package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestValidateCommandGuardrails(t *testing.T) {
	blocked := []string{
		"rm -rf /",
		"rm -fr ~",
		"sudo ls",
		"cat ~/.ssh/id_rsa",
		"security dump-keychain",
	}
	for _, cmd := range blocked {
		if err := ValidateCommand(cmd); err == nil {
			t.Fatalf("expected command %q to be blocked by guardrail", cmd)
		}
	}

	allowed := []string{
		"uname -a",
		"go test ./...",
		"ls -la /workspace/test",
		"rm -rf ./tmp_build",
	}
	for _, cmd := range allowed {
		if err := ValidateCommand(cmd); err != nil {
			t.Fatalf("expected command %q to be allowed, got %v", cmd, err)
		}
	}
}

func TestRewriteWorkspacePath(t *testing.T) {
	got := RewriteWorkspacePath("ls -la /workspace/src && cat /workspace/README.md", "/tmp/my-repo")
	want := "ls -la /tmp/my-repo/src && cat /tmp/my-repo/README.md"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExecuteJobWithSyncBarriers(t *testing.T) {
	cloud := &miniSyncServer{root: t.TempDir()}
	localDir := t.TempDir()
	ctx := context.Background()

	client := newMiniSyncClient(t, cloud)
	_, _ = client.Login(ctx, "admin", "admin123")
	_, _ = client.AcquireLock(ctx, "dev-local", "MacBook", "darwin", false)
	eng, err := NewEngine(localDir, client)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	// Simulate AI editing a file in cloud /workspace right before calling Bash
	_ = os.WriteFile(filepath.Join(cloud.root, "input.txt"), []byte("hello from cloud edit"), 0o644)

	var syncMu sync.Mutex
	res := ExecuteJob(ctx, eng, &syncMu, &ExecJob{
		ID:         "job-1",
		Username:   "admin",
		Command:    "cat input.txt && echo 'generated locally' > output.txt && uname -s",
		Cwd:        "/workspace",
		TimeoutSec: 15,
	})

	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello from cloud edit") {
		t.Fatalf("pre-exec sync barrier did not pull input.txt before command ran; stdout=%q", res.Stdout)
	}

	// Verify Post-Exec Sync Barrier pushed output.txt back to cloud /workspace immediately
	cloudOut, err := os.ReadFile(filepath.Join(cloud.root, "output.txt"))
	if err != nil {
		t.Fatalf("post-exec sync barrier did not push output.txt to cloud: %v", err)
	}
	if !strings.Contains(string(cloudOut), "generated locally") {
		t.Fatalf("unexpected cloud output.txt content: %q", string(cloudOut))
	}
}
