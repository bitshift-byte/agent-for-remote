package syncer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxOutputBytes = 2 * 1024 * 1024 // 2 MB cap per stream

var blockedCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f[a-z]*\s+(/|~|\$HOME)(\s|$)`),
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*f[a-z]*r[a-z]*\s+(/|~|\$HOME)(\s|$)`),
	regexp.MustCompile(`(?i)\bsudo\b`),
	regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`),
	regexp.MustCompile(`(?i)\bdd\s+if=`),
	regexp.MustCompile(`(?i)(~|\$HOME|/Users/[^/\s]+|/root)/\.ssh\b`),
	regexp.MustCompile(`(?i)/etc/shadow\b`),
	regexp.MustCompile(`(?i)\bsecurity\s+dump-keychain\b`),
	regexp.MustCompile(`(?i)\bshutdown\b|\breboot\b`),
}

// MountEngine pairs a mount namespace ("<device>/<repo>") with its local sync Engine and mutex.
type MountEngine struct {
	Mount  string
	Engine *Engine
	Mu     *sync.Mutex
}

// ValidateCommand applies local safety guardrails so cloud-forwarded commands stay scoped and non-destructive.
func ValidateCommand(command string) error {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return errors.New("empty command")
	}
	for _, re := range blockedCommandPatterns {
		if re.MatchString(trimmed) {
			return fmt.Errorf("blocked by local safety guardrail (matched %s)", re.String())
		}
	}
	return nil
}

// ResolveWorkDir maps the cloud container's /workspace[/sub] path to the local synced repository directory.
func ResolveWorkDir(localRoot, cloudCwd string) string {
	return ResolveWorkDirForMount(localRoot, "", cloudCwd)
}

// ResolveWorkDirForMount maps /workspace[/<mount>][/sub] to the corresponding local repository directory.
func ResolveWorkDirForMount(localRoot, mount, cloudCwd string) string {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return localRoot
	}
	c := strings.TrimSpace(cloudCwd)
	mountPrefix := "/workspace"
	if m := strings.Trim(strings.TrimSpace(mount), "/"); m != "" {
		mountPrefix = "/workspace/" + m
	}
	if c == "" || c == mountPrefix || c == mountPrefix+"/" || c == "/workspace" || c == "/workspace/" {
		return absRoot
	}
	if strings.HasPrefix(c, mountPrefix+"/") {
		rel := strings.TrimPrefix(c, mountPrefix+"/")
		joined := filepath.Clean(filepath.Join(absRoot, filepath.FromSlash(rel)))
		if joined == absRoot || strings.HasPrefix(joined, absRoot+string(filepath.Separator)) {
			if info, err := os.Stat(joined); err == nil && info.IsDir() {
				return joined
			}
		}
	}
	return absRoot
}

var workspaceTokenRe = regexp.MustCompile(`(^|[\s"'=:])/workspace(/[^\s"'&|;)]*)?`)

// RewriteWorkspacePath rewrites /workspace references inside a command string to the local repository path.
func RewriteWorkspacePath(command, localRoot string) string {
	return RewriteWorkspacePathForMount(command, localRoot, "")
}

// RewriteWorkspacePathForMount rewrites /workspace/<mount> (or /workspace) references to localRoot.
func RewriteWorkspacePathForMount(command, localRoot, mount string) string {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		absRoot = localRoot
	}
	mountPrefix := "/workspace"
	if m := strings.Trim(strings.TrimSpace(mount), "/"); m != "" {
		mountPrefix = "/workspace/" + m
	}
	return workspaceTokenRe.ReplaceAllStringFunc(command, func(match string) string {
		idx := strings.Index(match, "/workspace")
		prefix := match[:idx]
		wsPart := match[idx:]
		if wsPart == mountPrefix {
			return prefix + absRoot
		}
		if strings.HasPrefix(wsPart, mountPrefix+"/") {
			return prefix + absRoot + strings.TrimPrefix(wsPart, mountPrefix)
		}
		if mountPrefix == "/workspace" {
			return prefix + absRoot + strings.TrimPrefix(wsPart, "/workspace")
		}
		return match
	})
}

// ExecuteJob runs the full A+B pipeline for a single-mount engine.
func ExecuteJob(ctx context.Context, engine *Engine, syncMu *sync.Mutex, job *ExecJob) ExecResult {
	return ExecuteJobWithApproval(ctx, engine, syncMu, job, nil)
}

func ExecuteJobWithApproval(ctx context.Context, engine *Engine, syncMu *sync.Mutex, job *ExecJob, approvals *ApprovalManager) ExecResult {
	mount := ""
	if job != nil {
		mount = job.Mount
	}
	return executeOnMount(ctx, &MountEngine{Mount: mount, Engine: engine, Mu: syncMu}, job, approvals)
}

func executeOnMount(ctx context.Context, target *MountEngine, job *ExecJob, approvals *ApprovalManager) ExecResult {
	start := time.Now()
	res := ExecResult{JobID: job.ID}

	if target == nil || target.Engine == nil {
		res.Stderr = "[super-agent] no local repository mapped for mount " + job.Mount + "\n"
		res.ExitCode = 125
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	if err := ValidateCommand(job.Command); err != nil {
		res.Stderr = "[super-agent guardrail] " + err.Error() + "\n"
		res.ExitCode = 126
		res.DurationMs = time.Since(start).Milliseconds()
		log.Printf("[exec] ⛔ blocked job=%s cmd=%q reason=%v", job.ID, job.Command, err)
		return res
	}

	if approvals != nil {
		if reason, needs := NeedsLocalApproval(job.Command); needs {
			if err := approvals.Request(ctx, job, reason); err != nil {
				res.Stderr = fmt.Sprintf("[SUPER-AGENT APPROVAL REJECTED] 本地高危命令未获批准 (%s): %v\n", reason, err)
				res.ExitCode = 126
				res.DurationMs = time.Since(start).Milliseconds()
				return res
			}
		}
	}

	target.Mu.Lock()
	defer target.Mu.Unlock()

	// 1. Pre-Exec Sync Barrier (scoped strictly to the target repository)
	if preStats, err := target.Engine.SyncOnce(ctx); err != nil {
		log.Printf("[exec] pre-sync warning job=%s mount=%q err=%v", job.ID, target.Mount, err)
	} else {
		res.PreSynced = preStats.Pushed + preStats.Pulled + preStats.Deleted
	}

	// 2. Execute command locally inside the mapped repo directory
	workDir := ResolveWorkDirForMount(target.Engine.rootDir, target.Mount, job.Cwd)
	rewritten := RewriteWorkspacePathForMount(job.Command, target.Engine.rootDir, target.Mount)
	timeout := time.Duration(job.TimeoutSec) * time.Second
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 120 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Printf("[exec] ▶ running job=%s mount=%q cwd=%s cmd=%q", job.ID, target.Mount, workDir, rewritten)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd", "/C", rewritten)
	} else {
		cmd = exec.CommandContext(execCtx, "/bin/sh", "-c", rewritten)
	}
	cmd.Dir = workDir

	var stdoutBuf, stderrBuf limitedBuffer
	stdoutBuf.limit = maxOutputBytes
	stderrBuf.limit = maxOutputBytes
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	res.Stdout = stdoutBuf.String()
	res.Stderr = stderrBuf.String()
	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			res.ExitCode = 124
			res.Stderr += "\n[super-agent] command timed out\n"
		} else {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				res.ExitCode = exitErr.ExitCode()
			} else {
				res.ExitCode = 1
				res.Stderr += "\n" + err.Error() + "\n"
			}
		}
	}

	// 3. Post-Exec Sync Barrier
	if postStats, syncErr := target.Engine.SyncOnce(ctx); syncErr != nil {
		log.Printf("[exec] post-sync warning job=%s mount=%q err=%v", job.ID, target.Mount, syncErr)
	} else {
		res.PostSynced = postStats.Pushed + postStats.Pulled + postStats.Deleted
	}

	res.DurationMs = time.Since(start).Milliseconds()
	log.Printf("[exec] ✔ done job=%s mount=%q exit=%d duration=%dms pre_sync=%d post_sync=%d",
		job.ID, target.Mount, res.ExitCode, res.DurationMs, res.PreSynced, res.PostSynced)
	return res
}

// RunExecLoop continuously long-polls the gateway for cloud-forwarded commands and executes them locally.
func RunExecLoop(ctx context.Context, client *Client, engine *Engine, syncMu *sync.Mutex, onLockLost func()) {
	RunExecLoopWithApproval(ctx, client, engine, syncMu, nil, onLockLost)
}

func RunExecLoopWithApproval(ctx context.Context, client *Client, engine *Engine, syncMu *sync.Mutex, approvals *ApprovalManager, onLockLost func()) {
	me := &MountEngine{Mount: client.Mount(), Engine: engine, Mu: syncMu}
	mounts := map[string]*MountEngine{}
	if me.Mount != "" {
		mounts[me.Mount] = me
	}
	RunMultiExecLoop(ctx, client, client.deviceName, mounts, me, approvals, onLockLost)
}

// RunMultiExecLoop polls jobs for a device managing multiple mounted repositories and routes each job to its repo.
func RunMultiExecLoop(ctx context.Context, client *Client, deviceName string, mounts map[string]*MountEngine, defaultMount *MountEngine, approvals *ApprovalManager, onLockLost func()) {
	mountNames := make([]string, 0, len(mounts))
	for m := range mounts {
		if m != "" {
			mountNames = append(mountNames, m)
		}
	}
	sort.Strings(mountNames)
	if len(mountNames) > 0 {
		log.Printf("[exec] local command bridge online — routing %d mounts: %s", len(mountNames), strings.Join(mountNames, ", "))
	} else if defaultMount != nil && defaultMount.Engine != nil {
		log.Printf("[exec] local command bridge online — ready to execute cloud AI commands in %s", defaultMount.Engine.rootDir)
	}

	for {
		if ctx.Err() != nil {
			return
		}
		job, err := client.PollExecJobWithMounts(ctx, 25, deviceName, mountNames)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ErrLockLost) {
				if onLockLost != nil {
					onLockLost()
				}
				return
			}
			log.Printf("[exec] poll error: %v (retrying in 2s)", err)
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}
		if job == nil {
			continue
		}

		target := defaultMount
		if job.Mount != "" {
			if m, ok := mounts[job.Mount]; ok {
				target = m
			}
		}

		res := executeOnMount(ctx, target, job, approvals)
		if err := client.SubmitExecResult(ctx, res); err != nil {
			if errors.Is(err, ErrLockLost) {
				if onLockLost != nil {
					onLockLost()
				}
				return
			}
			log.Printf("[exec] failed to submit result for job=%s: %v", job.ID, err)
		}
	}
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	if lb.buf.Len() >= lb.limit {
		lb.truncated = true
		return len(p), nil
	}
	remain := lb.limit - lb.buf.Len()
	if len(p) > remain {
		lb.buf.Write(p[:remain])
		lb.truncated = true
		return len(p), nil
	}
	return lb.buf.Write(p)
}

func (lb *limitedBuffer) String() string {
	if lb.truncated {
		return lb.buf.String() + "\n... [output truncated at 2MB]\n"
	}
	return lb.buf.String()
}
