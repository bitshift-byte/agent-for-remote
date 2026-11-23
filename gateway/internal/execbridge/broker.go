package execbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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

type userQueue struct {
	jobs    chan *ExecJob
	pollers int
}

type Broker struct {
	mu      sync.Mutex
	queues  map[string]*userQueue
	waiters map[string]chan ExecResult
}

func NewBroker() *Broker {
	return &Broker{
		queues:  make(map[string]*userQueue),
		waiters: make(map[string]chan ExecResult),
	}
}

func (b *Broker) getOrCreateQueueLocked(username string) *userQueue {
	q, ok := b.queues[username]
	if !ok {
		q = &userQueue{jobs: make(chan *ExecJob, 16)}
		b.queues[username] = q
	}
	return q
}

func (b *Broker) HasActiveWorker(username string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	q, ok := b.queues[username]
	return ok && q.pollers > 0
}

// Poll blocks until a job arrives for username or ctx expires.
func (b *Broker) Poll(ctx context.Context, username string) (*ExecJob, error) {
	b.mu.Lock()
	q := b.getOrCreateQueueLocked(username)
	q.pollers++
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		q.pollers--
		b.mu.Unlock()
	}()

	select {
	case job := <-q.jobs:
		return job, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Submit dispatches a command to the user's connected local sync-agent and waits for its result.
func (b *Broker) Submit(ctx context.Context, username, command, cwd string, timeoutSec int) (*ExecResult, error) {
	if timeoutSec <= 0 || timeoutSec > 300 {
		timeoutSec = 120
	}

	jobID := newJobID()
	job := &ExecJob{
		ID:         jobID,
		Username:   username,
		Command:    command,
		Cwd:        cwd,
		TimeoutSec: timeoutSec,
	}

	resCh := make(chan ExecResult, 1)

	b.mu.Lock()
	q := b.getOrCreateQueueLocked(username)
	if q.pollers == 0 {
		b.mu.Unlock()
		return nil, ErrNoActiveWorker
	}
	b.waiters[jobID] = resCh
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiters, jobID)
		b.mu.Unlock()
	}()

	select {
	case q.jobs <- job:
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
