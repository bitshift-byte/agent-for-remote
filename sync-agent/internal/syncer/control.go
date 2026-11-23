package syncer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type LocalConfig struct {
	Server        string `json:"server"`
	Username      string `json:"username"`
	DeviceToken   string `json:"device_token,omitempty"`
	PayloadKeyB64 string `json:"payload_key_b64,omitempty"`
	Token         string `json:"token,omitempty"`
	Dir           string `json:"dir"`
	DeviceName    string `json:"device_name"`
	IntervalMs    int    `json:"interval_ms"`
}

type StatusSnapshot struct {
	State      string      `json:"state"` // "stopped", "starting", "running", "conflict", "lock_lost", "error"
	Running    bool        `json:"running"`
	Bound      bool        `json:"bound"`
	Encrypted  bool        `json:"encrypted"`
	LastError  string      `json:"last_error,omitempty"`
	LockedBy   *DeviceLock `json:"locked_by,omitempty"`
	Epoch      int64       `json:"epoch,omitempty"`
	Server     string      `json:"server"`
	Username   string      `json:"username"`
	HasToken   bool        `json:"has_token"`
	Dir        string      `json:"dir"`
	DeviceName string      `json:"device_name"`
	DeviceID   string      `json:"device_id"`
	OS         string      `json:"os"`
	Logs       []string    `json:"logs"`
}

type logRing struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newLogRing(max int) *logRing {
	return &logRing{max: max, lines: make([]string, 0, max)}
}

func (lr *logRing) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")
	if line != "" {
		lr.mu.Lock()
		if len(lr.lines) >= lr.max {
			copy(lr.lines, lr.lines[1:])
			lr.lines[len(lr.lines)-1] = line
		} else {
			lr.lines = append(lr.lines, line)
		}
		lr.mu.Unlock()
	}
	return len(p), nil
}

func (lr *logRing) Snapshot() []string {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	cp := make([]string, len(lr.lines))
	copy(cp, lr.lines)
	return cp
}

// Controller manages the lifecycle of the sync & local-exec worker for desktop tray UIs.
type Controller struct {
	mu        sync.Mutex
	cfgPath   string
	cfg       LocalConfig
	identity  *DeviceIdentity
	state     string
	lastError string
	lockedBy  *DeviceLock
	epoch     int64
	cancel    context.CancelFunc
	doneCh    chan struct{}
	logs      *logRing
}

func NewController() *Controller {
	cfgDir := appConfigDir()
	if cfgDir == "" {
		cfgDir = "."
	}
	cfgPath := filepath.Join(cfgDir, "config.json")

	ring := newLogRing(200)
	log.SetOutput(io.MultiWriter(os.Stderr, ring))

	c := &Controller{
		cfgPath: cfgPath,
		state:   "stopped",
		logs:    ring,
		cfg: LocalConfig{
			Server:     "http://localhost:8080",
			IntervalMs: 800,
		},
	}
	c.loadConfig()
	id, err := LoadOrCreateDeviceIdentity(cfgDir, c.cfg.DeviceName)
	if err == nil {
		c.identity = id
		if c.cfg.DeviceName == "" {
			c.cfg.DeviceName = id.DeviceName
		}
	}
	return c
}

func (c *Controller) loadConfig() {
	raw, err := os.ReadFile(c.cfgPath)
	if err != nil {
		return
	}
	var loaded LocalConfig
	if json.Unmarshal(raw, &loaded) == nil {
		if loaded.Server != "" {
			c.cfg.Server = loaded.Server
		}
		if loaded.Username != "" {
			c.cfg.Username = loaded.Username
		}
		if loaded.DeviceToken != "" {
			c.cfg.DeviceToken = loaded.DeviceToken
		}
		if loaded.PayloadKeyB64 != "" {
			c.cfg.PayloadKeyB64 = loaded.PayloadKeyB64
		}
		if loaded.Token != "" {
			c.cfg.Token = loaded.Token
		}
		if loaded.Dir != "" {
			c.cfg.Dir = loaded.Dir
		}
		if loaded.DeviceName != "" {
			c.cfg.DeviceName = loaded.DeviceName
		}
		if loaded.IntervalMs > 0 {
			c.cfg.IntervalMs = loaded.IntervalMs
		}
	}
}

func (c *Controller) saveConfigLocked() error {
	_ = os.MkdirAll(filepath.Dir(c.cfgPath), 0o700)
	raw, err := json.MarshalIndent(c.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.cfgPath, raw, 0o600)
}

func (c *Controller) Status() StatusSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	devID := ""
	devOS := runtime.GOOS
	if c.identity != nil {
		devID = c.identity.DeviceID
		devOS = c.identity.OS
	}
	var lockedCopy *DeviceLock
	if c.lockedBy != nil {
		cp := *c.lockedBy
		lockedCopy = &cp
	}
	bound := c.cfg.DeviceToken != "" || c.cfg.Token != ""
	return StatusSnapshot{
		State:      c.state,
		Running:    c.state == "running" || c.state == "starting",
		Bound:      bound,
		Encrypted:  c.cfg.PayloadKeyB64 != "",
		LastError:  c.lastError,
		LockedBy:   lockedCopy,
		Epoch:      c.epoch,
		Server:     c.cfg.Server,
		Username:   c.cfg.Username,
		HasToken:   bound,
		Dir:        c.cfg.Dir,
		DeviceName: c.cfg.DeviceName,
		DeviceID:   devID,
		OS:         devOS,
		Logs:       c.logs.Snapshot(),
	}
}

func (c *Controller) Pair(input, fallbackServer, dir, deviceName string) error {
	serverURL, code, err := ParseBindInput(input, fallbackServer)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if strings.TrimSpace(deviceName) != "" && c.identity != nil {
		c.identity.DeviceName = strings.TrimSpace(deviceName)
	}
	id := c.identity
	c.mu.Unlock()
	if id == nil {
		return errors.New("device identity not initialized")
	}

	client := NewClient(serverURL)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	claimed, err := client.ClaimPairingCode(ctx, code, id.DeviceID, id.DeviceName, id.OS)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.cfg.Server = serverURL
	c.cfg.Username = claimed.Username
	c.cfg.DeviceToken = claimed.DeviceToken
	c.cfg.PayloadKeyB64 = claimed.PayloadKeyB64
	c.cfg.Token = "" // Clear any legacy password once bound via DeviceToken
	if strings.TrimSpace(dir) != "" {
		c.cfg.Dir = strings.TrimSpace(dir)
	}
	if strings.TrimSpace(deviceName) != "" {
		c.cfg.DeviceName = strings.TrimSpace(deviceName)
	}
	err = c.saveConfigLocked()
	hasDir := c.cfg.Dir != ""
	c.mu.Unlock()
	if err != nil {
		return err
	}

	log.Printf("[pair] bound device %q to account %q on %s", id.DeviceName, claimed.Username, serverURL)
	if hasDir {
		return c.Start(false)
	}
	return nil
}

func (c *Controller) Unbind() error {
	c.Stop()
	c.mu.Lock()
	c.cfg.DeviceToken = ""
	c.cfg.PayloadKeyB64 = ""
	c.cfg.Token = ""
	c.cfg.Username = ""
	err := c.saveConfigLocked()
	c.mu.Unlock()
	log.Println("[pair] unbound local device")
	return err
}

func (c *Controller) UpdateConfig(next LocalConfig) error {
	c.mu.Lock()
	wasRunning := c.cancel != nil
	if strings.TrimSpace(next.Server) != "" {
		c.cfg.Server = strings.TrimSpace(next.Server)
	}
	if strings.TrimSpace(next.Username) != "" {
		c.cfg.Username = strings.TrimSpace(next.Username)
	}
	if next.Token != "" {
		c.cfg.Token = next.Token
	}
	if strings.TrimSpace(next.Dir) != "" {
		c.cfg.Dir = strings.TrimSpace(next.Dir)
	}
	if strings.TrimSpace(next.DeviceName) != "" {
		c.cfg.DeviceName = strings.TrimSpace(next.DeviceName)
		if c.identity != nil {
			c.identity.DeviceName = c.cfg.DeviceName
		}
	}
	if next.IntervalMs > 0 {
		c.cfg.IntervalMs = next.IntervalMs
	}
	err := c.saveConfigLocked()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if wasRunning {
		c.Stop()
		return c.Start(false)
	}
	return nil
}

func (c *Controller) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	done := c.doneCh
	c.cancel = nil
	c.doneCh = nil
	c.state = "stopped"
	c.lastError = ""
	c.lockedBy = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(4 * time.Second):
		}
	}
}

func (c *Controller) Start(force bool) error {
	c.Stop()

	c.mu.Lock()
	cfg := c.cfg
	if cfg.Server == "" || (cfg.DeviceToken == "" && (cfg.Username == "" || cfg.Token == "")) || cfg.Dir == "" {
		c.state = "error"
		c.lastError = "请先完成 6 位配对码绑定并选择本地仓库目录"
		c.mu.Unlock()
		return errors.New(c.lastError)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.cancel = cancel
	c.doneCh = done
	c.state = "starting"
	c.lastError = ""
	c.lockedBy = nil
	id := c.identity
	c.mu.Unlock()

	go c.runWorker(ctx, done, cfg, id, force)
	return nil
}

func (c *Controller) runWorker(ctx context.Context, done chan struct{}, cfg LocalConfig, id *DeviceIdentity, force bool) {
	defer close(done)

	if id == nil {
		var err error
		id, err = LoadOrCreateDeviceIdentity(cfg.Dir, cfg.DeviceName)
		if err != nil {
			c.setError("error", fmt.Sprintf("device identity error: %v", err), nil)
			return
		}
	}

	client := NewClient(cfg.Server)
	if cfg.DeviceToken != "" {
		client.SetSession(cfg.DeviceToken, id.DeviceID, "")
		if cfg.PayloadKeyB64 != "" {
			if key, err := base64.StdEncoding.DecodeString(cfg.PayloadKeyB64); err == nil && len(key) == 32 {
				client.SetPayloadKey(key)
				log.Printf("[tray] 🔒 AES-256-GCM payload encryption enabled for %s on %s", cfg.Username, cfg.Server)
			}
		} else {
			log.Printf("[tray] authenticating with bound device token for %s on %s", cfg.Username, cfg.Server)
		}
	} else {
		if _, err := client.Login(ctx, cfg.Username, cfg.Token); err != nil {
			c.setError("error", fmt.Sprintf("登录失败: %v", err), nil)
			return
		}
		log.Printf("[tray] logged in as %s on %s", cfg.Username, cfg.Server)
	}

	acq, err := client.AcquireLock(ctx, id.DeviceID, id.DeviceName, id.OS, force)
	if err != nil {
		var conflictErr *LockConflictError
		if errors.As(err, &conflictErr) {
			c.setError("conflict", "另一台设备正在同步，请确认是否接管", conflictErr.LockedBy)
			return
		}
		if strings.Contains(err.Error(), "401") {
			c.setError("error", "设备凭证已失效或已在网页端解绑，请重新输入 6 位配对码绑定", nil)
			return
		}
		c.setError("error", fmt.Sprintf("获取设备锁失败: %v", err), nil)
		return
	}

	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.ReleaseLock(releaseCtx)
		log.Println("[lock] released active device lock")
	}()

	engine, err := NewEngine(cfg.Dir, client)
	if err != nil {
		c.setError("error", fmt.Sprintf("初始化同步引擎失败: %v", err), nil)
		return
	}

	if acq.TakenOver {
		log.Printf("[lock] took over from %q — pulling latest cloud files...", acq.PreviousDev)
		if _, err := engine.InitialTakeoverPull(ctx); err != nil {
			c.setError("error", fmt.Sprintf("接管拉取失败: %v", err), nil)
			return
		}
	}

	c.mu.Lock()
	c.state = "running"
	c.lastError = ""
	c.lockedBy = nil
	if acq.Lock != nil {
		c.epoch = acq.Lock.Epoch
	}
	c.mu.Unlock()

	var syncMu sync.Mutex
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()

	// Reconnect Sync-Before-Exec: reconcile all cloud changes locally BEFORE registering as an active exec worker.
	syncMu.Lock()
	initErr := runSyncPass(workerCtx, engine)
	syncMu.Unlock()
	if initErr != nil {
		if errors.Is(initErr, ErrLockLost) {
			c.setError("lock_lost", "同步权已被其他设备接管，本机已自动暂停", nil)
			return
		}
		if errors.Is(initErr, ErrMassDeleteBlocked) {
			log.Printf("[sync] ⛔ %v", initErr)
			c.setError("error", "⚠️ 已拦截云端批量删除本地文件（云端目录可能被清空或重置），同步已自动暂停保护本地代码："+initErr.Error(), nil)
			return
		}
		log.Printf("[sync] initial reconnect sync warning: %v", initErr)
	}

	go RunExecLoop(workerCtx, client, engine, &syncMu, func() {
		log.Println("[lock] another device took over — paused automatically")
		c.setError("lock_lost", "同步权已被其他设备接管，本机已自动暂停", nil)
		cancelWorker()
	})

	interval := time.Duration(cfg.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 800 * time.Millisecond
	}
	syncTicker := time.NewTicker(interval)
	hbTicker := time.NewTicker(15 * time.Second)
	defer syncTicker.Stop()
	defer hbTicker.Stop()

	for {
		select {
		case <-workerCtx.Done():
			return
		case <-hbTicker.C:
			if err := client.Heartbeat(workerCtx); err != nil {
				if errors.Is(err, ErrLockLost) {
					c.setError("lock_lost", "同步权已被其他设备接管，本机已自动暂停", nil)
					return
				}
				log.Printf("[lock] heartbeat warning: %v", err)
			}
		case <-syncTicker.C:
			syncMu.Lock()
			err := runSyncPass(workerCtx, engine)
			syncMu.Unlock()
			if err != nil {
				if errors.Is(err, ErrLockLost) {
					c.setError("lock_lost", "同步权已被其他设备接管，本机已自动暂停", nil)
					return
				}
				if errors.Is(err, ErrMassDeleteBlocked) {
					log.Printf("[sync] ⛔ %v", err)
					c.setError("error", "⚠️ 已拦截云端批量删除本地文件（云端目录可能被清空或重置），同步已自动暂停保护本地代码："+err.Error(), nil)
					return
				}
				log.Printf("[sync] pass error: %v", err)
			}
		}
	}
}

func (c *Controller) setError(state, msg string, lockedBy *DeviceLock) {
	c.mu.Lock()
	c.state = state
	c.lastError = msg
	c.lockedBy = lockedBy
	c.cancel = nil
	c.mu.Unlock()
}

// ServeControlAPI exposes the local-only HTTP control API for the Tauri desktop tray app.
func (c *Controller) ServeControlAPI(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/local/status", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		writeLocalJSON(w, http.StatusOK, c.Status())
	}))
	mux.HandleFunc("/local/pair", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeLocalJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			Input      string `json:"input"`
			Server     string `json:"server"`
			Dir        string `json:"dir"`
			DeviceName string `json:"device_name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
			writeLocalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if err := c.Pair(req.Input, req.Server, req.Dir, req.DeviceName); err != nil {
			writeLocalJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeLocalJSON(w, http.StatusOK, c.Status())
	}))
	mux.HandleFunc("/local/unbind", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeLocalJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		_ = c.Unbind()
		writeLocalJSON(w, http.StatusOK, c.Status())
	}))
	mux.HandleFunc("/local/config", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeLocalJSON(w, http.StatusOK, c.Status())
			return
		}
		if r.Method != http.MethodPost {
			writeLocalJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var next LocalConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&next); err != nil {
			writeLocalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if err := c.UpdateConfig(next); err != nil {
			writeLocalJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeLocalJSON(w, http.StatusOK, c.Status())
	}))
	mux.HandleFunc("/local/start", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeLocalJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var body struct {
			Force bool `json:"force"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := c.Start(body.Force); err != nil {
			writeLocalJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeLocalJSON(w, http.StatusOK, c.Status())
	}))
	mux.HandleFunc("/local/stop", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeLocalJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		c.Stop()
		writeLocalJSON(w, http.StatusOK, c.Status())
	}))
	mux.HandleFunc("/local/reset-baseline", c.withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeLocalJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		c.Stop()
		c.mu.Lock()
		dir := c.cfg.Dir
		srv := c.cfg.Server
		c.mu.Unlock()
		if dir != "" {
			if abs, err := filepath.Abs(dir); err == nil {
				_ = os.Remove(stateFilePath(strings.TrimRight(strings.TrimSpace(srv), "/"), abs))
			}
		}
		log.Printf("[sync] reset local sync baseline for %s (server=%s)", dir, srv)
		writeLocalJSON(w, http.StatusOK, c.Status())
	}))

	// Auto-start on boot if already bound and directory is configured
	st := c.Status()
	if st.Server != "" && st.Bound && st.Dir != "" {
		log.Printf("[tray] auto-starting saved workspace sync: %s", st.Dir)
		_ = c.Start(false)
	}

	// Auto-exit when parent tray app terminates (prevents orphaned zombie processes on Ctrl+C / crash)
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		log.Println("[tray] parent process closed stdin — exiting sync-agent")
		c.Stop()
		os.Exit(0)
	}()

	log.Printf("[tray] sync-agent local control API listening on http://%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (c *Controller) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func writeLocalJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
