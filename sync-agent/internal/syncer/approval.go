package syncer

import (
	"context"
	"errors"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrApprovalRejected = errors.New("command rejected by user on local machine")
	ErrApprovalTimeout  = errors.New("local command approval timed out")
)

type approvalRule struct {
	re     *regexp.Regexp
	reason string
}

// Tier-2 sensitive commands that are allowed ONLY after explicit local approval in the tray UI.
var approvalRequiredRules = []approvalRule{
	{regexp.MustCompile(`(?i)\bgit\s+push\b.*(--force|-f\b|--force-with-lease)`), "强制推送 Git 历史 (git push --force)"},
	{regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`), "丢弃本地未提交改动 (git reset --hard)"},
	{regexp.MustCompile(`(?i)\bgit\s+clean\s+-[a-z]*f`), "强制清理未跟踪文件 (git clean -f)"},
	{regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f|\brm\s+-[a-z]*f[a-z]*r`), "递归强制删除文件/目录 (rm -rf)"},
	{regexp.MustCompile(`(?i)\b(drop\s+table|drop\s+database|truncate\s+table)\b`), "破坏性数据库操作 (DROP/TRUNCATE)"},
	{regexp.MustCompile(`(?i)\b(kubectl|helm)\s+(delete|uninstall)\b`), "删除 Kubernetes 资源"},
	{regexp.MustCompile(`(?i)\bterraform\s+destroy\b`), "销毁云基础设施 (terraform destroy)"},
	{regexp.MustCompile(`(?i)\bdocker\s+(rm|rmi|system\s+prune|volume\s+rm)\b`), "删除 Docker 容器/镜像/卷"},
}

// NeedsLocalApproval returns (reason, true) when a command matches Tier-2 sensitive patterns.
func NeedsLocalApproval(command string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	for _, rule := range approvalRequiredRules {
		if rule.re.MatchString(trimmed) {
			return rule.reason, true
		}
	}
	return "", false
}

type PendingApproval struct {
	JobID     string    `json:"job_id"`
	Command   string    `json:"command"`
	Cwd       string    `json:"cwd"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

type approvalEntry struct {
	info PendingApproval
	ch   chan bool
}

// ApprovalManager holds pending Tier-2 sensitive commands awaiting user Allow/Deny in the local tray app.
type ApprovalManager struct {
	mu      sync.Mutex
	pending map[string]*approvalEntry
	timeout time.Duration
}

func NewApprovalManager(timeout time.Duration) *ApprovalManager {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &ApprovalManager{
		pending: make(map[string]*approvalEntry),
		timeout: timeout,
	}
}

func (am *ApprovalManager) Pending() []PendingApproval {
	am.mu.Lock()
	defer am.mu.Unlock()
	out := make([]PendingApproval, 0, len(am.pending))
	for _, e := range am.pending {
		out = append(out, e.info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (am *ApprovalManager) Request(ctx context.Context, job *ExecJob, reason string) error {
	entry := &approvalEntry{
		info: PendingApproval{
			JobID:     job.ID,
			Command:   job.Command,
			Cwd:       job.Cwd,
			Reason:    reason,
			CreatedAt: time.Now().UTC(),
		},
		ch: make(chan bool, 1),
	}

	am.mu.Lock()
	am.pending[job.ID] = entry
	am.mu.Unlock()

	defer func() {
		am.mu.Lock()
		delete(am.pending, job.ID)
		am.mu.Unlock()
	}()

	log.Printf("[approval] ⚠ waiting for local user approval job=%s reason=%q cmd=%q", job.ID, reason, job.Command)

	timer := time.NewTimer(am.timeout)
	defer timer.Stop()

	select {
	case approved := <-entry.ch:
		if !approved {
			log.Printf("[approval] ❌ user rejected job=%s", job.ID)
			return ErrApprovalRejected
		}
		log.Printf("[approval] ✅ user approved job=%s", job.ID)
		return nil
	case <-timer.C:
		log.Printf("[approval] ⏱ approval timed out job=%s", job.ID)
		return ErrApprovalTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (am *ApprovalManager) Decide(jobID string, approve bool) bool {
	am.mu.Lock()
	entry, ok := am.pending[jobID]
	am.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case entry.ch <- approve:
	default:
	}
	return true
}
