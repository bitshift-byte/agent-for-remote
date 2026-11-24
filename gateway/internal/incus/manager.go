package incus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"super-agent/gateway/internal/config"
	"super-agent/gateway/internal/runtime"
)

var validNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// Manager orchestrates per-user KVM virtual machines via Incus.
type Manager struct {
	client    *Client
	cfg       *config.Config
	signToken func(username string) (string, error)

	mu         sync.Mutex
	userLocks  map[string]*sync.Mutex
	lastActive map[string]time.Time
	ipCache    map[string]string
}

func NewManager(client *Client, cfg *config.Config, signToken func(username string) (string, error)) *Manager {
	return &Manager{
		client:     client,
		cfg:        cfg,
		signToken:  signToken,
		userLocks:  make(map[string]*sync.Mutex),
		lastActive: make(map[string]time.Time),
		ipCache:    make(map[string]string),
	}
}

func (m *Manager) IsMock() bool {
	return false
}

func (m *Manager) Config() *config.Config {
	return m.cfg
}

func (m *Manager) lockForUser(username string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.userLocks[username]
	if !ok {
		l = &sync.Mutex{}
		m.userLocks[username] = l
	}
	return l
}

func (m *Manager) TouchActivity(username string) {
	m.mu.Lock()
	m.lastActive[username] = time.Now().UTC()
	m.mu.Unlock()
}

func (m *Manager) LastActive(username string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastActive[username]
}

func SanitizeUsername(username string) string {
	s := strings.ToLower(strings.TrimSpace(username))
	s = validNameRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "user"
	}
	return s
}

func VMName(username string) string {
	return "sa-vm-" + SanitizeUsername(username)
}

func (m *Manager) UpstreamURL(username string) string {
	m.mu.Lock()
	ip := m.ipCache[username]
	m.mu.Unlock()
	if ip == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if st, err := m.client.GetInstanceState(ctx, VMName(username)); err == nil {
			ip = extractIPv4(st)
			if ip != "" {
				m.mu.Lock()
				m.ipCache[username] = ip
				m.mu.Unlock()
			}
		}
	}
	if ip == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:3000", ip)
}

func (m *Manager) GetStatus(ctx context.Context, username string) (*runtime.DesktopStatus, error) {
	name := VMName(username)
	status := &runtime.DesktopStatus{
		Username:      username,
		ContainerName: name,
		Runtime:       "incus-kvm",
		State:         "not_created",
		Ready:         false,
		DesktopURL:    runtime.DesktopSubfolder,
		MemoryMB:      m.cfg.DesktopMemoryMB,
		CPUs:          m.cfg.DesktopCPUs,
	}

	st, err := m.client.GetInstanceState(ctx, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return status, nil
		}
		return nil, err
	}

	switch strings.ToLower(st.Status) {
	case "running":
		status.State = "running"
		if ip := extractIPv4(st); ip != "" {
			m.mu.Lock()
			m.ipCache[username] = ip
			m.mu.Unlock()
			status.Ready = m.isDesktopReady(ctx, ip)
		}
	case "frozen":
		status.State = "paused"
	default:
		status.State = "stopped"
	}
	return status, nil
}

func (m *Manager) EnsureRunning(ctx context.Context, username string) (*runtime.DesktopStatus, error) {
	ulock := m.lockForUser(username)
	ulock.Lock()
	defer ulock.Unlock()

	name := VMName(username)
	st, err := m.client.GetInstanceState(ctx, name)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err := m.createUserVM(ctx, username, name); err != nil {
			return nil, err
		}
		if err := m.client.ChangeState(ctx, name, "start", 60, false); err != nil {
			return nil, err
		}
	} else {
		switch strings.ToLower(st.Status) {
		case "frozen":
			if err := m.client.ChangeState(ctx, name, "unfreeze", 30, false); err != nil {
				return nil, err
			}
		case "running":
		default:
			if err := m.client.ChangeState(ctx, name, "start", 60, false); err != nil {
				return nil, err
			}
		}
	}

	ip, err := m.waitUntilReady(ctx, name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.ipCache[username] = ip
	m.mu.Unlock()
	m.TouchActivity(username)

	return &runtime.DesktopStatus{
		Username:      username,
		ContainerName: name,
		Runtime:       "incus-kvm",
		State:         "running",
		Ready:         true,
		DesktopURL:    runtime.DesktopSubfolder,
		MemoryMB:      m.cfg.DesktopMemoryMB,
		CPUs:          m.cfg.DesktopCPUs,
	}, nil
}

func (m *Manager) StopDesktop(ctx context.Context, username string) error {
	ulock := m.lockForUser(username)
	ulock.Lock()
	defer ulock.Unlock()

	m.mu.Lock()
	delete(m.ipCache, username)
	m.mu.Unlock()

	err := m.client.ChangeState(ctx, VMName(username), "stop", 30, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (m *Manager) StartIdleReaper(ctx context.Context) {
	if m.cfg.DesktopIdlePause <= 0 {
		log.Println("[incus-reaper] idle auto-freeze disabled")
		return
	}
	log.Printf("[incus-reaper] idle auto-freeze enabled: %s", m.cfg.DesktopIdlePause)
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reapIdleOnce(ctx)
		}
	}
}

func (m *Manager) reapIdleOnce(ctx context.Context) {
	now := time.Now().UTC()
	m.mu.Lock()
	candidates := make([]string, 0)
	for user, ts := range m.lastActive {
		if now.Sub(ts) >= m.cfg.DesktopIdlePause {
			candidates = append(candidates, user)
		}
	}
	m.mu.Unlock()

	for _, user := range candidates {
		ulock := m.lockForUser(user)
		ulock.Lock()
		last := m.LastActive(user)
		if time.Since(last) < m.cfg.DesktopIdlePause {
			ulock.Unlock()
			continue
		}
		name := VMName(user)
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		st, err := m.client.GetInstanceState(checkCtx, name)
		if err == nil && strings.EqualFold(st.Status, "running") {
			if freezeErr := m.client.ChangeState(checkCtx, name, "freeze", 15, false); freezeErr != nil {
				log.Printf("[incus-reaper] freeze %s failed: %v", name, freezeErr)
			} else {
				log.Printf("[incus-reaper] auto-froze idle KVM VM %s (user=%s)", name, user)
			}
		}
		cancel()
		ulock.Unlock()
	}
}

func (m *Manager) createUserVM(ctx context.Context, username, name string) error {
	safeUser := SanitizeUsername(username)
	cpus := int(math.Max(1, math.Round(m.cfg.DesktopCPUs)))
	memMB := m.cfg.DesktopMemoryMB
	if memMB < 1536 {
		memMB = 2048
	}

	internalTok := ""
	if m.signToken != nil {
		if tok, err := m.signToken(username); err == nil {
			internalTok = tok
		}
	}
	egress := m.cfg.UserEgressProxies[username]
	if egress == "" {
		egress = m.cfg.UserEgressProxies["*"]
	}

	vmCfg := map[string]string{
		"limits.cpu":                  strconv.Itoa(cpus),
		"limits.memory":               fmt.Sprintf("%dMiB", memMB),
		"user.super-agent.user":       safeUser,
		"user.super-agent.tz":         m.cfg.DesktopTZ,
		"user.super-agent.gateway":    m.cfg.VMGatewayURL,
		"user.super-agent.token":      internalTok,
		"user.super-agent.egress_url": egress,
	}

	devices := map[string]map[string]string{
		"root": {
			"type": "disk",
			"path": "/",
			"pool": m.cfg.IncusStoragePool,
		},
		"eth0": {
			"type":    "nic",
			"network": m.cfg.IncusNetwork,
			"name":    "eth0",
		},
	}

	hostWorkspaceBase := m.cfg.WorkspaceHostDir
	if hostWorkspaceBase == "" {
		hostWorkspaceBase = m.cfg.WorkspaceDataDir
	}
	if hostWorkspaceBase != "" {
		userHostDir := filepath.Join(hostWorkspaceBase, safeUser)
		_ = os.MkdirAll(userHostDir, 0o775)
		devices["workspace"] = map[string]string{
			"type":   "disk",
			"source": userHostDir,
			"path":   "/workspace",
		}
	}

	log.Printf("[incus] creating KVM VM=%s user=%s image=%s cpus=%d mem=%dMB", name, username, m.cfg.IncusVMImage, cpus, memMB)

	return m.client.CreateVM(ctx, CreateInstanceRequest{
		Name:    name,
		Type:    "virtual-machine",
		Config:  vmCfg,
		Devices: devices,
		Source: InstanceSource{
			Type:  "image",
			Alias: m.cfg.IncusVMImage,
		},
	})
}

func (m *Manager) waitUntilReady(ctx context.Context, vmName string) (string, error) {
	timeout := m.cfg.StartTimeout
	if timeout < 90*time.Second {
		timeout = 90 * time.Second
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		st, err := m.client.GetInstanceState(ctx, vmName)
		if err == nil && strings.EqualFold(st.Status, "running") {
			if ip := extractIPv4(st); ip != "" && m.isDesktopReady(ctx, ip) {
				return ip, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("KVM VM %s did not become ready within %s", vmName, timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) isDesktopReady(ctx context.Context, ip string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, fmt.Sprintf("http://%s:3000%s", ip, runtime.DesktopSubfolder), nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func extractIPv4(st *InstanceState) string {
	if st == nil {
		return ""
	}
	for iface, netInfo := range st.Network {
		if iface == "lo" {
			continue
		}
		for _, addr := range netInfo.Addresses {
			if addr.Family == "inet" && addr.Scope == "global" && addr.Address != "" {
				return addr.Address
			}
		}
	}
	return ""
}
