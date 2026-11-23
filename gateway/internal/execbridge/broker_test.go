package execbridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBrokerDispatchAndComplete(t *testing.T) {
	b := NewBroker()

	// No worker connected yet
	if _, err := b.Submit(context.Background(), "admin", "uname -a", "/workspace", 10); !errors.Is(err, ErrNoActiveWorker) {
		t.Fatalf("expected ErrNoActiveWorker, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start simulated local sync-agent worker
	workerDone := make(chan error, 1)
	go func() {
		job, err := b.Poll(ctx, "admin")
		if err != nil {
			workerDone <- err
			return
		}
		workerDone <- b.Complete(ExecResult{
			JobID:    job.ID,
			Stdout:   "Darwin MacBook 24.0 arm64\n",
			ExitCode: 0,
		})
	}()

	// Wait briefly for poller to register
	deadline := time.Now().Add(time.Second)
	for !b.HasActiveWorker("admin") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	res, err := b.Submit(ctx, "admin", "uname -a", "/workspace", 10)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "Darwin MacBook 24.0 arm64\n" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := <-workerDone; err != nil {
		t.Fatalf("worker failed: %v", err)
	}
}
