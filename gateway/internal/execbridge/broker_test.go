package execbridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBrokerDispatchAndComplete(t *testing.T) {
	b := NewBroker()

	if _, err := b.Submit(context.Background(), "admin", "uname -a", "/workspace", 10); !errors.Is(err, ErrNoActiveWorker) {
		t.Fatalf("expected ErrNoActiveWorker, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

func TestBrokerRoutesByMountPrefixAcrossDevices(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	workDone := make(chan error, 1)
	go func() {
		job, err := b.PollDevice(ctx, "jack", "dev-work", "work-mac", []string{
			"work-mac/api-service",
			"work-mac/web-admin",
			"work-mac/data-pipeline",
		})
		if err != nil {
			workDone <- err
			return
		}
		workDone <- b.Complete(ExecResult{
			JobID:    job.ID,
			Stdout:   "from-work:" + job.Mount,
			ExitCode: 0,
		})
	}()

	personalDone := make(chan error, 1)
	go func() {
		job, err := b.PollDevice(ctx, "jack", "dev-personal", "personal-mac", []string{
			"personal-mac/super-agent",
			"personal-mac/side-project",
		})
		if err != nil {
			personalDone <- err
			return
		}
		personalDone <- b.Complete(ExecResult{
			JobID:    job.ID,
			Stdout:   "from-personal:" + job.Mount,
			ExitCode: 0,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for len(b.OnlineDevices("jack")) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(b.OnlineDevices("jack")) != 2 {
		t.Fatalf("expected 2 online devices, got %d", len(b.OnlineDevices("jack")))
	}

	resWork, err := b.Submit(ctx, "jack", "go test ./...", "/workspace/work-mac/api-service/internal", 10)
	if err != nil || resWork.Stdout != "from-work:work-mac/api-service" {
		t.Fatalf("expected work-mac route, got res=%+v err=%v", resWork, err)
	}

	resPersonal, err := b.Submit(ctx, "jack", "npm test", "/workspace/personal-mac/side-project", 10)
	if err != nil || resPersonal.Stdout != "from-personal:personal-mac/side-project" {
		t.Fatalf("expected personal-mac route, got res=%+v err=%v", resPersonal, err)
	}

	if err := <-workDone; err != nil {
		t.Fatalf("work worker err: %v", err)
	}
	if err := <-personalDone; err != nil {
		t.Fatalf("personal worker err: %v", err)
	}
}
