package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	ErrNoActiveWorker = errors.New("no local sync-agent tunnel worker connected for this user")
	ErrReqNotFound    = errors.New("tunnel request not found or expired")
	ErrReqTimeout     = errors.New("local tunnel request timed out")
)

type TunnelRequest struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Port    int                 `json:"port"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
	BodyB64 string              `json:"body_b64,omitempty"`
}

type TunnelResponse struct {
	ReqID   string              `json:"req_id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	BodyB64 string              `json:"body_b64,omitempty"`
	Error   string              `json:"error,omitempty"`
}

type userQueue struct {
	reqs    chan *TunnelRequest
	pollers int
}

type Broker struct {
	mu      sync.Mutex
	queues  map[string]*userQueue
	waiters map[string]chan TunnelResponse
}

func NewBroker() *Broker {
	return &Broker{
		queues:  make(map[string]*userQueue),
		waiters: make(map[string]chan TunnelResponse),
	}
}

func (b *Broker) getOrCreateQueueLocked(username string) *userQueue {
	q, ok := b.queues[username]
	if !ok {
		q = &userQueue{reqs: make(chan *TunnelRequest, 32)}
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

func (b *Broker) Poll(ctx context.Context, username string) (*TunnelRequest, error) {
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
	case req := <-q.reqs:
		return req, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *Broker) Submit(ctx context.Context, username string, req TunnelRequest, timeout time.Duration) (*TunnelResponse, error) {
	if timeout <= 0 || timeout > 60*time.Second {
		timeout = 30 * time.Second
	}
	req.ID = newReqID()
	resCh := make(chan TunnelResponse, 1)

	b.mu.Lock()
	q := b.getOrCreateQueueLocked(username)
	if q.pollers == 0 {
		b.mu.Unlock()
		return nil, ErrNoActiveWorker
	}
	b.waiters[req.ID] = resCh
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.waiters, req.ID)
		b.mu.Unlock()
	}()

	select {
	case q.reqs <- &req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-resCh:
		return &res, nil
	case <-timer.C:
		return nil, ErrReqTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *Broker) Complete(res TunnelResponse) error {
	b.mu.Lock()
	ch, ok := b.waiters[res.ReqID]
	b.mu.Unlock()
	if !ok {
		return ErrReqNotFound
	}
	select {
	case ch <- res:
	default:
	}
	return nil
}

func newReqID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "tun-" + hex.EncodeToString(b)
}
