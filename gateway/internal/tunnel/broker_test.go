package tunnel

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestTunnelBrokerSubmitPollComplete(t *testing.T) {
	b := NewBroker()

	// No worker connected -> ErrNoActiveWorker
	if _, err := b.Submit(context.Background(), "admin", TunnelRequest{Method: "GET", Port: 3000, Path: "/"}, time.Second); !errors.Is(err, ErrNoActiveWorker) {
		t.Fatalf("expected ErrNoActiveWorker, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start simulated local worker
	ready := make(chan struct{})
	go func() {
		close(ready)
		req, err := b.Poll(ctx, "admin")
		if err != nil {
			return
		}
		_ = b.Complete(TunnelResponse{
			ReqID:   req.ID,
			Status:  200,
			Headers: map[string][]string{"Content-Type": {"text/plain"}},
			BodyB64: base64.StdEncoding.EncodeToString([]byte("hello from local mac")),
		})
	}()
	<-ready
	time.Sleep(20 * time.Millisecond)

	if !b.HasActiveWorker("admin") {
		t.Fatalf("expected active tunnel worker")
	}

	resp, err := b.Submit(ctx, "admin", TunnelRequest{Method: "GET", Port: 3000, Path: "/health"}, 3*time.Second)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	if resp.Status != 200 || string(raw) != "hello from local mac" {
		t.Fatalf("unexpected tunnel response: %+v", resp)
	}
}
