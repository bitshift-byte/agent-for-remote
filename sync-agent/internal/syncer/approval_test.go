package syncer

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApprovalManagerApproveAndReject(t *testing.T) {
	if _, ok := NeedsLocalApproval("go test ./..."); ok {
		t.Fatalf("normal go test should not require approval")
	}
	if _, ok := NeedsLocalApproval("git push origin main --force"); !ok {
		t.Fatalf("git push --force should require approval")
	}
	if _, ok := NeedsLocalApproval("rm -rf ./dist"); !ok {
		t.Fatalf("rm -rf should require approval")
	}

	am := NewApprovalManager(3 * time.Second)
	job1 := &ExecJob{ID: "job-approve", Command: "git push -f origin dev", Cwd: "/workspace"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- am.Request(context.Background(), job1, "force push")
	}()

	time.Sleep(30 * time.Millisecond)
	if len(am.Pending()) != 1 || am.Pending()[0].JobID != "job-approve" {
		t.Fatalf("expected 1 pending approval, got %+v", am.Pending())
	}
	if !am.Decide("job-approve", true) {
		t.Fatalf("expected Decide to succeed")
	}
	if err := <-errCh; err != nil {
		t.Fatalf("expected nil after approve, got %v", err)
	}

	// Reject flow
	job2 := &ExecJob{ID: "job-reject", Command: "rm -rf build", Cwd: "/workspace"}
	go func() {
		errCh <- am.Request(context.Background(), job2, "rm -rf")
	}()
	time.Sleep(30 * time.Millisecond)
	am.Decide("job-reject", false)
	if err := <-errCh; !errors.Is(err, ErrApprovalRejected) {
		t.Fatalf("expected ErrApprovalRejected, got %v", err)
	}
}
