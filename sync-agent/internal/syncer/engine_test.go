package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// miniSyncServer simulates the gateway's /api/sync/* endpoints for end-to-end syncer tests.
type miniSyncServer struct {
	root       string
	mu         sync.Mutex
	lock       *DeviceLock
	epoch      int64
	leaseToken string
}

type handlerRoundTripper struct {
	handler http.Handler
}

func (rt handlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rt.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func newMiniSyncClient(t *testing.T, m *miniSyncServer) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"username": "admin", "token": "test-jwt"})
	})
	mux.HandleFunc("/api/sync/lock/acquire", m.handleAcquire)
	mux.HandleFunc("/api/sync/lock/heartbeat", m.handleHeartbeat)
	mux.HandleFunc("/api/sync/manifest", m.handleManifest)
	mux.HandleFunc("/api/sync/file", m.handleFile)
	mux.HandleFunc("/api/sync/dir", m.handleDir)

	c := NewClient("http://in-memory.local")
	c.httpClient = &http.Client{Transport: handlerRoundTripper{handler: mux}}
	return c
}

func (m *miniSyncServer) handleAcquire(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		OS         string `json:"os"`
		Force      bool   `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lock != nil && m.lock.DeviceID != req.DeviceID && !req.Force {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "conflict", "locked_by": m.lock})
		return
	}
	prev := ""
	takenOver := false
	if m.lock != nil && m.lock.DeviceID != req.DeviceID {
		prev = m.lock.DeviceName
		takenOver = true
	}
	m.epoch++
	m.leaseToken = "lease-" + strconv.FormatInt(m.epoch, 10)
	m.lock = &DeviceLock{
		Username:      "admin",
		DeviceID:      req.DeviceID,
		DeviceName:    req.DeviceName,
		OS:            req.OS,
		LeaseToken:    m.leaseToken,
		Epoch:         m.epoch,
		LastHeartbeat: time.Now().UTC(),
	}
	_ = json.NewEncoder(w).Encode(AcquireResponse{Lock: m.lock, TakenOver: takenOver, PreviousDev: prev})
}

func (m *miniSyncServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID   string `json:"device_id"`
		LeaseToken string `json:"lease_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lock == nil || m.lock.DeviceID != req.DeviceID || m.leaseToken != req.LeaseToken {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (m *miniSyncServer) handleManifest(w http.ResponseWriter, r *http.Request) {
	files := make(map[string]FileEntry)
	dirs := make([]string, 0)
	_ = filepath.WalkDir(m.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == m.root {
			return nil
		}
		rel, _ := filepath.Rel(m.root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}
		info, _ := d.Info()
		h, _ := hashFile(p)
		files[rel] = FileEntry{
			Path:        rel,
			SHA256:      h,
			Size:        info.Size(),
			ModTimeNano: info.ModTime().UTC().UnixNano(),
			Mode:        uint32(info.Mode().Perm()),
		}
		return nil
	})
	_ = json.NewEncoder(w).Encode(ManifestResponse{Username: "admin", Files: files, Dirs: dirs})
}

func (m *miniSyncServer) handleDir(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	full := filepath.Join(m.root, filepath.FromSlash(rel))
	m.mu.Lock()
	ok := m.lock != nil && m.lock.DeviceID == r.Header.Get("X-Device-ID") && m.leaseToken == r.Header.Get("X-Lease-Token")
	m.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusConflict)
		return
	}
	switch r.Method {
	case http.MethodPut:
		_ = os.MkdirAll(full, 0o755)
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		_ = os.RemoveAll(full)
		w.WriteHeader(http.StatusOK)
	}
}

func (m *miniSyncServer) handleFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	full := filepath.Join(m.root, filepath.FromSlash(rel))
	switch r.Method {
	case http.MethodGet:
		raw, err := os.ReadFile(full)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		info, _ := os.Stat(full)
		w.Header().Set("X-File-ModTime", strconv.FormatInt(info.ModTime().UTC().UnixNano(), 10))
		w.Header().Set("X-File-Mode", "644")
		_, _ = w.Write(raw)
	case http.MethodPut:
		m.mu.Lock()
		ok := m.lock != nil && m.lock.DeviceID == r.Header.Get("X-Device-ID") && m.leaseToken == r.Header.Get("X-Lease-Token")
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		raw, _ := io.ReadAll(r.Body)
		_ = os.WriteFile(full, raw, 0o644)
		h, _ := hashFile(full)
		info, _ := os.Stat(full)
		_ = json.NewEncoder(w).Encode(FileEntry{
			Path:        rel,
			SHA256:      h,
			Size:        info.Size(),
			ModTimeNano: info.ModTime().UTC().UnixNano(),
			Mode:        0o644,
		})
	case http.MethodDelete:
		m.mu.Lock()
		ok := m.lock != nil && m.lock.DeviceID == r.Header.Get("X-Device-ID") && m.leaseToken == r.Header.Get("X-Lease-Token")
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		_ = os.Remove(full)
		w.WriteHeader(http.StatusOK)
	}
}

func TestBidirectionalSyncAndDeviceTakeover(t *testing.T) {
	cloud := &miniSyncServer{root: t.TempDir()}

	ctx := context.Background()
	dirA := t.TempDir()
	dirB := t.TempDir()

	// Device A logs in & acquires lock
	clientA := newMiniSyncClient(t, cloud)
	if _, err := clientA.Login(ctx, "admin", "admin123"); err != nil {
		t.Fatalf("login A failed: %v", err)
	}
	if _, err := clientA.AcquireLock(ctx, "dev-a", "Office-Mac", "darwin", false); err != nil {
		t.Fatalf("acquire A failed: %v", err)
	}
	engA, err := NewEngine(dirA, clientA)
	if err != nil {
		t.Fatalf("NewEngine A failed: %v", err)
	}

	// Write a tracked file and an ignored file on Device A
	_ = os.MkdirAll(filepath.Join(dirA, "node_modules", "foo"), 0o755)
	_ = os.WriteFile(filepath.Join(dirA, "node_modules", "foo", "index.js"), []byte("ignored"), 0o644)
	_ = os.WriteFile(filepath.Join(dirA, "app.go"), []byte("package main // v1"), 0o644)

	stats, err := engA.SyncOnce(ctx)
	if err != nil || stats.Pushed != 1 {
		t.Fatalf("expected 1 pushed file, got stats=%+v err=%v", stats, err)
	}
	if _, err := os.Stat(filepath.Join(cloud.root, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("node_modules should not be synced to cloud")
	}

	// Simulate cloud agent creating an empty folder and a file in cloud /workspace
	_ = os.MkdirAll(filepath.Join(cloud.root, "empty_cloud_dir"), 0o755)
	_ = os.MkdirAll(filepath.Join(dirA, "empty_local_dir"), 0o755)
	_ = os.WriteFile(filepath.Join(cloud.root, "agent_output.md"), []byte("# Generated by Claude"), 0o644)
	stats, err = engA.SyncOnce(ctx)
	if err != nil || stats.Pulled != 2 || stats.Pushed != 1 {
		t.Fatalf("expected 2 pulled (file+dir) and 1 pushed (dir), got stats=%+v err=%v", stats, err)
	}
	if _, err := os.Stat(filepath.Join(dirA, "empty_cloud_dir")); err != nil {
		t.Fatalf("expected empty_cloud_dir to be pulled locally: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cloud.root, "empty_local_dir")); err != nil {
		t.Fatalf("expected empty_local_dir to be pushed to cloud: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dirA, "agent_output.md"))
	if !strings.Contains(string(got), "Generated by Claude") {
		t.Fatalf("unexpected pulled content: %q", string(got))
	}

	// Device B tries to acquire without force -> must fail with ErrLockConflict
	clientB := newMiniSyncClient(t, cloud)
	_, _ = clientB.Login(ctx, "admin", "admin123")
	if _, err := clientB.AcquireLock(ctx, "dev-b", "Home-Mac", "darwin", false); !errors.Is(err, ErrLockConflict) {
		t.Fatalf("expected ErrLockConflict for Device B, got %v", err)
	}

	// Device B forces takeover and performs initial one-way pull
	acqB, err := clientB.AcquireLock(ctx, "dev-b", "Home-Mac", "darwin", true)
	if err != nil || !acqB.TakenOver {
		t.Fatalf("expected takeover success, got acq=%+v err=%v", acqB, err)
	}
	engB, _ := NewEngine(dirB, clientB)
	pullStats, err := engB.InitialTakeoverPull(ctx)
	if err != nil || pullStats.Pulled != 4 {
		t.Fatalf("expected Device B to pull 2 cloud files + 2 dirs, got stats=%+v err=%v", pullStats, err)
	}

	// Device A's next heartbeat must detect ErrLockLost so it auto-pauses
	if err := clientA.Heartbeat(ctx); !errors.Is(err, ErrLockLost) {
		t.Fatalf("expected Device A heartbeat to fail with ErrLockLost, got %v", err)
	}
}

func TestMassDeletionGuardrailBlocksEmptyCloudWipe(t *testing.T) {
	cloud := &miniSyncServer{root: t.TempDir()}
	localDir := t.TempDir()
	ctx := context.Background()

	client := newMiniSyncClient(t, cloud)
	if _, err := client.Login(ctx, "admin", "admin123"); err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if _, err := client.AcquireLock(ctx, "dev-guard", "Guard-Mac", "darwin", false); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	eng, err := NewEngine(localDir, client)
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.ResetBaseline() })

	// Create 6 local files and sync them to cloud
	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go"}
	for _, name := range files {
		_ = os.WriteFile(filepath.Join(localDir, name), []byte("content-"+name), 0o644)
	}
	stats, err := eng.SyncOnce(ctx)
	if err != nil || stats.Pushed != len(files) {
		t.Fatalf("expected %d pushed, got stats=%+v err=%v", len(files), stats, err)
	}

	// Normal single-file deletion on cloud should still sync cleanly
	_ = os.Remove(filepath.Join(cloud.root, "f.go"))
	stats, err = eng.SyncOnce(ctx)
	if err != nil || stats.Deleted != 1 {
		t.Fatalf("expected single cloud delete to sync cleanly, got stats=%+v err=%v", stats, err)
	}
	if _, err := os.Stat(filepath.Join(localDir, "f.go")); !os.IsNotExist(err) {
		t.Fatalf("expected f.go to be deleted locally")
	}

	// Simulate catastrophic cloud workspace wipe (all remaining 5 files disappear on cloud)
	for _, name := range files[:5] {
		_ = os.Remove(filepath.Join(cloud.root, name))
	}
	if _, err := eng.SyncOnce(ctx); !errors.Is(err, ErrMassDeleteBlocked) {
		t.Fatalf("expected ErrMassDeleteBlocked when cloud becomes empty, got %v", err)
	}
	// Verify all 5 remaining local files are completely preserved on disk
	for _, name := range files[:5] {
		if _, err := os.Stat(filepath.Join(localDir, name)); err != nil {
			t.Fatalf("guardrail failed: local file %s was deleted!", name)
		}
	}
}
