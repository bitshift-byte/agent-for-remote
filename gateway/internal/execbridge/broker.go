package execbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoActiveWorker = errors.New("no local sync-agent connected for this user")
	ErrJobNotFound    = errors.New("exec job not found or expired")
	ErrJobTimeout     = errors.New("local command execution timed out")
)

type ExecJob struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	Mount      string `json:"mount,omitempty"`
	TimeoutSec int    `json:"timeout_sec"`
}

type ExecResult struct {
	JobID      string `json:"job_id"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	PreSynced  int    `json:"pre_synced"`
	PostSynced int    `json:"post_synced"`
	Error      string `json:"error,omitempty"`
}

type OnlineDeviceInfo struct {
	DeviceID   string   `json:"device_id"`
	DeviceName string   `json:"device_name"`
	Mounts     []string `json:"mounts"`
}

type deviceWorker struct {
	deviceID   string
	deviceName string
	mounts     []string
	jobs       chan *ExecJob
	pollers    int
}

type Broker struct {
	mu      sync.Mutex
	workers map[string]map[string]*deviceWorker // username -> deviceID -> worker
	waiters map[string]chan ExecResult
}

func NewBroker() *Broker {
	return &Broker{
		workers: make(map[string]map[string]*deviceWorker),
		waiters: make(map[string]chan ExecResult),
	}
}

func (b *Broker) getOrCreateWorkerLocked(username, deviceID, deviceName string, mounts []string) *deviceWorker {
	if deviceID == "" {
		deviceID = "default"
	}
	userWorkers, ok := b.workers[username]
	if !ok {
		userWorkers = make(map[string]*deviceWorker)
		b.workers[username] = userWorkers
	}
	w, ok := userWorkers[deviceID]
	if !ok {
		w = &deviceWorker{
			deviceID: deviceID,
			jobs:     make(chan *ExecJob, 16),
		}
		userWorkers[deviceID] = w
	}
	if deviceName != "" {
		w.deviceName = deviceName
	}
	if mounts != nil {
		clean := make([]string, 0, len(mounts))
		for _, m := range mounts {
			m = strings.Trim(strings.TrimSpace(strings.ReplaceAll(m, "\\", "/")), "/")
			if m != "" {
				clean = append(clean, m)
			}
		}
		w.mounts = clean
	}
	return w
}

func (b *Broker) HasActiveWorker(username string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, w := range b.workers[username] {
		if w.pollers > 0 {
			return true
		}
	}
	return false
}

// OnlineDevices returns all currently polling devices and their registered mount paths for username.
func (b *Broker) OnlineDevices(username string) []OnlineDeviceInfo {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]OnlineDeviceInfo, 0)
	for _, w := range b.workers[username] {
		if w.pollers <= 0 {
			continue
		}
		mountsCopy := make([]string, len(w.mounts))
		copy(mountsCopy, w.mounts)
		out = append(out, OnlineDeviceInfo{
			DeviceID:   w.deviceID,
			DeviceName: w.deviceName,
			Mounts:     mountsCopy,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviceName != out[j].DeviceName {
			return out[i].DeviceName < out[j].DeviceName
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out
}

// Poll blocks until a job arrives for username (legacy single-device helper).
func (b *Broker) Poll(ctx context.Context, username string) (*ExecJob, error) {
	return b.PollDevice(ctx, username, "default", "", nil)
}

// PollDevice blocks until a job arrives for username's specific deviceID, registering its active mounts.
func (b *Broker) PollDevice(ctx context.Context, username, deviceID, deviceName string, mounts []string) (*ExecJob, error) {
	b.mu.Lock()
	w := b.getOrCreateWorkerLocked(username, deviceID, deviceName, mounts)
	w.pollers++
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		w.pollers--
		b.mu.Unlock()
	}()

	select {
	case job := <-w.jobs:
		return job, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// selectWorkerLocked routes a command cwd to the online device owning the matching mount prefix.
func (b *Broker) selectWorkerLocked(username, cwd string) (*deviceWorker, string) {
	userWorkers := b.workers[username]
	if len(userWorkers) == 0 {
		return nil, ""
	}

	relCwd := workspaceRelativePath(cwd)

	// 1. Longest mount prefix match across all online devices.
	var bestWorker *deviceWorker
	bestMount := ""
	if relCwd != "" {
		for _, w := range userWorkers {
			if w.pollers <= 0 {
				continue
			}
			for _, m := range w.mounts {
				if relCwd == m || strings.HasPrefix(relCwd, m+"/") {
					if len(m) > len(bestMount) {
						bestMount = m
						bestWorker = w
					}
				}
			}
		}
		if bestWorker != nil {
			return bestWorker, bestMount
		}
	}

	// 2. Fallback for legacy root /workspace or single-device sessions.
	if def, ok := userWorkers["default"]; ok && def.pollers > 0 {
		return def, ""
	}
	var online []*deviceWorker
	for _, w := range userWorkers {
		if w.pollers > 0 {
			online = append(online, w)
		}
	}
	if len(online) == 0 {
		return nil, ""
	}
	if len(online) == 1 {
		return online[0], ""
	}
	sort.Slice(online, func(i, j int) bool { return online[i].deviceID < online[j].deviceID })
	return online[0], ""
}

func workspaceRelativePath(cwd string) string {
	c := strings.TrimSpace(strings.ReplaceAll(cwd, "\\", "/"))
	if c == "" {
		return ""
	}
	c = path.Clean(c)
	if c == "/workspace" {
		return ""
	}
	if strings.HasPrefix(c, "/workspace/") {
		return strings.TrimPrefix(c, "/workspace/")
	}
	return ""
}

// Submit dispatches a command to the matching local sync-agent for cwd and waits for its result.
func (b *Broker) Submit(ctx context.Context, username, command, cwd string, timeoutSec int) (*ExecResult, error) {
	if timeoutSec <= 0 || timeoutSec > 300 {
		timeoutSec = 120
	}

	jobID := newJobID()
	resCh := make(chan ExecResult, 1)

	b.mu.Lock()
	targetWorker, matchedMount := b.selectWorkerLocked(username, cwd)
	if targetWorker == nil {
		b.mu.Unlock()
		return nil, ErrNoActiveWorker
	}
	job := &ExecJob{
		ID:         jobID,
		Username:   username,
		Command:    command,
		Cwd:        cwd,
		Mount:      matchedMount,
		TimeoutSec: timeoutSec,
	}
	b.waiters[jobID] = resCh
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiters, jobID)
		b.mu.Unlock()
	}()

	select {
	case targetWorker.jobs <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	waitTimer := time.NewTimer(time.Duration(timeoutSec+15) * time.Second)
	defer waitTimer.Stop()

	select {
	case res := <-resCh:
		return &res, nil
	case <-waitTimer.C:
		return nil, ErrJobTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Complete delivers a command result from the local sync-agent back to the waiting caller.
func (b *Broker) Complete(res ExecResult) error {
	b.mu.Lock()
	ch, ok := b.waiters[res.JobID]
	b.mu.Unlock()
	if !ok {
		return ErrJobNotFound
	}
	select {
	case ch <- res:
	default:
	}
	return nil
}

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "job-" + hex.EncodeToString(b)
}
