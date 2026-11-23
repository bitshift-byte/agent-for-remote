package syncsvc

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIgnoreRulesAndPathSafety(t *testing.T) {
	if !IsIgnored("node_modules/react/index.js") {
		t.Fatalf("expected node_modules to be ignored")
	}
	if !IsIgnored("backend/.venv/bin/python") {
		t.Fatalf("expected .venv to be ignored")
	}
	if !IsIgnored("rust/target/debug/app") {
		t.Fatalf("expected target to be ignored")
	}
	if IsIgnored("src/main.go") {
		t.Fatalf("expected src/main.go NOT to be ignored")
	}
	if _, err := NormalizeRelPath("../etc/passwd"); err == nil {
		t.Fatalf("expected path traversal to be rejected")
	}
}

func TestActiveDeviceLockTakeover(t *testing.T) {
	lm := NewLockManager(30 * time.Second)

	resA, _, err := lm.Acquire("admin", AcquireRequest{
		DeviceID:   "dev-office",
		DeviceName: "Office-Mac",
		OS:         "darwin",
	})
	if err != nil || resA.TakenOver {
		t.Fatalf("expected initial lock acquire to succeed without takeover, got err=%v", err)
	}

	// Second device without force should get conflict
	_, holder, err := lm.Acquire("admin", AcquireRequest{
		DeviceID:   "dev-home",
		DeviceName: "Home-Mac",
		OS:         "darwin",
		Force:      false,
	})
	if err != ErrLockConflict || holder == nil || holder.DeviceName != "Office-Mac" {
		t.Fatalf("expected conflict held by Office-Mac, got err=%v holder=%+v", err, holder)
	}

	// Second device with force takes over
	resB, _, err := lm.Acquire("admin", AcquireRequest{
		DeviceID:   "dev-home",
		DeviceName: "Home-Mac",
		OS:         "darwin",
		Force:      true,
	})
	if err != nil || !resB.TakenOver || resB.PreviousDev != "Office-Mac" {
		t.Fatalf("expected force takeover from Office-Mac, got res=%+v err=%v", resB, err)
	}

	// Old device write/heartbeat must now fail with ErrLockConflict
	if err := lm.VerifyWrite("admin", "dev-office", resA.Lock.LeaseToken); err != ErrLockConflict {
		t.Fatalf("expected old device write to be rejected with ErrLockConflict, got %v", err)
	}
	if err := lm.VerifyWrite("admin", "dev-home", resB.Lock.LeaseToken); err != nil {
		t.Fatalf("expected new device write to succeed, got %v", err)
	}
}

func TestWorkspaceManifestAndFileOps(t *testing.T) {
	tmpDir := t.TempDir()
	svc := NewService(tmpDir, NewLockManager(time.Minute))

	entry, err := svc.WriteFile("admin", "app/hello.txt", bytes.NewBufferString("hello cloud"), 0, 0o644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if entry.Size != 11 {
		t.Fatalf("expected size 11, got %d", entry.Size)
	}

	// Create an ignored file directly to verify manifest skips it
	userDir, _ := svc.UserWorkspaceDir("admin")
	_ = os.MkdirAll(filepath.Join(userDir, "node_modules", "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(userDir, "node_modules", "pkg", "index.js"), []byte("x"), 0o644)

	manifest, err := svc.BuildManifest("admin")
	if err != nil {
		t.Fatalf("BuildManifest failed: %v", err)
	}
	if len(manifest.Files) != 1 {
		t.Fatalf("expected 1 tracked file in manifest, got %d: %+v", len(manifest.Files), manifest.Files)
	}
	if _, ok := manifest.Files["app/hello.txt"]; !ok {
		t.Fatalf("expected app/hello.txt in manifest")
	}

	if err := svc.DeleteFile("admin", "app/hello.txt"); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
	manifest2, _ := svc.BuildManifest("admin")
	if len(manifest2.Files) != 0 {
		t.Fatalf("expected 0 files after delete, got %d", len(manifest2.Files))
	}
}
