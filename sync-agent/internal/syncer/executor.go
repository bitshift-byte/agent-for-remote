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
// Any path outside /workspace is safely clamped to localRoot.
func ResolveWorkDir(localRoot, cloudCwd string) string {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		return localRoot
	}
	c := strings.TrimSpace(cloudCwd)
	if c == "" || c == "/workspace" || c == "/workspace/" {
		return absRoot
	}
	if strings.HasPrefix(c, "/workspace/") {
		rel := strings.TrimPrefix(c, "/workspace/")
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

// RewriteWorkspacePath rewrites /workspace references inside a command string to the local repository path
// so cloud AI commands referencing /workspace work transparently on the user's local machine.
func RewriteWorkspacePath(command, localRoot string) string {
	absRoot, err := filepath.Abs(localRoot)
	if err != nil {
		absRoot = localRoot
	}
	return workspaceTokenRe.ReplaceAllStringFunc(command, func(match string) string {
		idx := strings.Index(match, "/workspace")
		prefix := match[:idx]
		suffix := match[idx+len("/workspace"):]
		return prefix + absRoot + suffix
	})
}

// ExecuteJob runs the full A+B pipeline:
//   1. Pre-Exec Sync Barrier (pull latest cloud edits into local repo)
//   2. Safety check + execute command locally in the mapped repo folder
//   3. Post-Exec Sync Barrier (push command-generated files back to cloud /workspace)
func ExecuteJob(ctx context.Context, engine *Engine, syncMu *sync.Mutex, job *ExecJob) ExecResult {
	start := time.Now()
	res := ExecResult{JobID: job.ID}

	if err := ValidateCommand(job.Command); err != nil {
		res.Stderr = "[super-agent guardrail] " + err.Error() + "\n"
		res.ExitCode = 126
		res.DurationMs = time.Since(start).Milliseconds()
		log.Printf("[exec] ⛔ blocked job=%s cmd=%q reason=%v", job.ID, job.Command, err)
		return res
	}

	syncMu.Lock()
	defer syncMu.Unlock()

	// 1. Pre-Exec Sync Barrier
	if preStats, err := engine.SyncOnce(ctx); err != nil {
		log.Printf("[exec] pre-sync warning job=%s err=%v", job.ID, err)
	} else {
		res.PreSynced = preStats.Pushed + preStats.Pulled + preStats.Deleted
	}

	// 2. Execute command locally inside the mapped repo directory
	workDir := ResolveWorkDir(engine.rootDir, job.Cwd)
	rewritten := RewriteWorkspacePath(job.Command, engine.rootDir)
	timeout := time.Duration(job.TimeoutSec) * time.Second
	if timeout <= 0 || timeout > 5*time.Minute {
		timeout = 120 * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Printf("[exec] ▶ running job=%s cwd=%s cmd=%q", job.ID, workDir, rewritten)
	cmd := exec.CommandContext(execCtx, "/bin/sh", "-c", rewritten)
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
	if postStats, syncErr := engine.SyncOnce(ctx); syncErr != nil {
		log.Printf("[exec] post-sync warning job=%s err=%v", job.ID, syncErr)
	} else {
		res.PostSynced = postStats.Pushed + postStats.Pulled + postStats.Deleted
	}

	res.DurationMs = time.Since(start).Milliseconds()
	log.Printf("[exec] ✔ done job=%s exit=%d duration=%dms pre_sync=%d post_sync=%d",
		job.ID, res.ExitCode, res.DurationMs, res.PreSynced, res.PostSynced)
	return res
}

// RunExecLoop continuously long-polls the gateway for cloud-forwarded commands and executes them locally.
func RunExecLoop(ctx context.Context, client *Client, engine *Engine, syncMu *sync.Mutex, onLockLost func()) {
	log.Printf("[exec] local command bridge online — ready to execute cloud AI commands in %s", engine.rootDir)
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := client.PollExecJob(ctx, 25)
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

		res := ExecuteJob(ctx, engine, syncMu, job)
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
