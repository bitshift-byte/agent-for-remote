package docker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"super-agent/gateway/internal/config"
)

const DesktopSubfolder = "/desktop/"

var validNameRe = regexp.MustCompile(`[^a-z0-9_-]+`)

type DesktopStatus struct {
	Username      string `json:"username"`
	ContainerName string `json:"container_name"`
	State         string `json:"state"` // "running", "paused", "stopped", "not_created"
	Ready         bool   `json:"ready"`
	DesktopURL    string `json:"desktop_url"`
	MemoryMB      int64  `json:"memory_mb"`
	CPUs          float64 `json:"cpus"`
}

type Manager struct {
	client    *Client
	cfg       *config.Config
	signToken func(username string) (string, error)

	mu         sync.Mutex
	userLocks  map[string]*sync.Mutex
	lastActive map[string]time.Time
	mockStates map[string]string
}

func NewManager(client *Client, cfg *config.Config, signToken func(username string) (string, error)) *Manager {
	return &Manager{
		client:     client,
		cfg:        cfg,
		signToken:  signToken,
		userLocks:  make(map[string]*sync.Mutex),
		lastActive: make(map[string]time.Time),
		mockStates: make(map[string]string),
	}
}

func (m *Manager) IsMock() bool {
	return m.cfg.MockDesktop
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

func ContainerName(username string) string {
	return "sa-desktop-" + SanitizeUsername(username)
}

func (m *Manager) UpstreamURL(username string) string {
	return fmt.Sprintf("http://%s:3000", ContainerName(username))
}

func (m *Manager) GetStatus(ctx context.Context, username string) (*DesktopStatus, error) {
	name := ContainerName(username)
	status := &DesktopStatus{
		Username:      username,
		ContainerName: name,
		State:         "not_created",
		Ready:         false,
		DesktopURL:    DesktopSubfolder,
		MemoryMB:      m.cfg.DesktopMemoryMB,
		CPUs:          m.cfg.DesktopCPUs,
	}

	if m.cfg.MockDesktop {
		m.mu.Lock()
		st, ok := m.mockStates[username]
		m.mu.Unlock()
		if ok {
			status.State = st
			status.Ready = st == "running"
		}
		return status, nil
	}

	info, err := m.client.InspectContainer(ctx, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return status, nil
		}
		return nil, err
	}

	switch {
	case info.State.Paused:
		status.State = "paused"
	case info.State.Running:
		status.State = "running"
		status.Ready = m.isKasmReady(ctx, name)
	default:
		status.State = "stopped"
	}
	return status, nil
}

func (m *Manager) EnsureRunning(ctx context.Context, username string) (*DesktopStatus, error) {
	ulock := m.lockForUser(username)
	ulock.Lock()
	defer ulock.Unlock()

	name := ContainerName(username)
	if m.cfg.MockDesktop {
		select {
		case <-time.After(1200 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		m.mu.Lock()
		m.mockStates[username] = "running"
		m.mu.Unlock()
		m.TouchActivity(username)
		return &DesktopStatus{
			Username:      username,
			ContainerName: name,
			State:         "running",
			Ready:         true,
			DesktopURL:    DesktopSubfolder,
			MemoryMB:      m.cfg.DesktopMemoryMB,
			CPUs:          m.cfg.DesktopCPUs,
		}, nil
	}

	if err := m.client.EnsureNetwork(ctx, m.cfg.DockerNetwork); err != nil {
		return nil, err
	}

	info, err := m.client.InspectContainer(ctx, name)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err := m.createDesktopContainer(ctx, username, name); err != nil {
			return nil, err
		}
		if err := m.client.StartContainer(ctx, name); err != nil {
			return nil, err
		}
	} else if info.State.Paused {
		if err := m.client.UnpauseContainer(ctx, name); err != nil {
			return nil, err
		}
	} else if !info.State.Running {
		if err := m.client.StartContainer(ctx, name); err != nil {
			return nil, err
		}
	}

	if err := m.waitUntilReady(ctx, name); err != nil {
		return nil, err
	}

	m.TouchActivity(username)
	return &DesktopStatus{
		Username:      username,
		ContainerName: name,
		State:         "running",
		Ready:         true,
		DesktopURL:    DesktopSubfolder,
		MemoryMB:      m.cfg.DesktopMemoryMB,
		CPUs:          m.cfg.DesktopCPUs,
	}, nil
}

func (m *Manager) StopDesktop(ctx context.Context, username string) error {
	ulock := m.lockForUser(username)
	ulock.Lock()
	defer ulock.Unlock()

	if m.cfg.MockDesktop {
		m.mu.Lock()
		m.mockStates[username] = "stopped"
		m.mu.Unlock()
		return nil
	}

	return m.client.StopContainer(ctx, ContainerName(username), 10)
}

func (m *Manager) createDesktopContainer(ctx context.Context, username, name string) error {
	safeUser := SanitizeUsername(username)

	env := []string{
		"PUID=1000",
		"PGID=1000",
		"TZ=" + m.cfg.DesktopTZ,
		"SUBFOLDER=" + DesktopSubfolder,
		"TITLE=Super-Agent (" + username + ")",
		"SA_GATEWAY_URL=http://super-agent-gateway:" + m.cfg.Port,
	}
	if m.signToken != nil {
		if tok, err := m.signToken(username); err == nil && tok != "" {
			env = append(env, "SA_INTERNAL_TOKEN="+tok)
		}
	}
	if m.cfg.AnthropicAPIKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+m.cfg.AnthropicAPIKey)
	}
	if m.cfg.AnthropicBaseURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+m.cfg.AnthropicBaseURL)
	}
	if m.cfg.OpenAIAPIKey != "" {
		env = append(env, "OPENAI_API_KEY="+m.cfg.OpenAIAPIKey)
	}
	if m.cfg.OpenAIBaseURL != "" {
		env = append(env, "OPENAI_BASE_URL="+m.cfg.OpenAIBaseURL)
	}
	if m.cfg.DesktopDPI != "" {
		env = append(env, "DESKTOP_DPI="+m.cfg.DesktopDPI)
	}
	if m.cfg.DesktopFontSize != "" {
		env = append(env, "DESKTOP_FONT_SIZE="+m.cfg.DesktopFontSize)
	}
	if m.cfg.DesktopTheme != "" {
		env = append(env, "DESKTOP_THEME="+m.cfg.DesktopTheme)
	}

	workspaceBind := fmt.Sprintf("sa-workspace-%s:/workspace", safeUser)
	if m.cfg.WorkspaceHostDir != "" {
		workspaceBind = fmt.Sprintf("%s/%s:/workspace", m.cfg.WorkspaceHostDir, safeUser)
	}
	log.Printf("[docker] creating container=%s user=%s image=%s workspace=%s mem=%dMB cpus=%.1f",
		name, username, m.cfg.DesktopImage, workspaceBind, m.cfg.DesktopMemoryMB, m.cfg.DesktopCPUs)

	req := CreateContainerRequest{
		Image: m.cfg.DesktopImage,
		Env:   env,
		Labels: map[string]string{
			"super-agent.managed": "true",
			"super-agent.user":    safeUser,
		},
		HostConfig: HostConfig{
			// Separate persistent volumes:
			// - workspace: shared host dir (<WORKSPACE_HOST_DIR>/<user>:/workspace) when configured for cloud sync,
			//   or Docker named volume (sa-workspace-<user>:/workspace) otherwise.
			// - config: isolated per-user config volume (browser cookies, ~/.claude, ~/.codex, XFCE & IME config).
			Binds: []string{
				workspaceBind,
				fmt.Sprintf("sa-config-%s:/config", safeUser),
			},
			Memory:   m.cfg.DesktopMemoryMB * 1024 * 1024,
			NanoCpus: int64(m.cfg.DesktopCPUs * 1e9),
			ShmSize:  m.cfg.DesktopShmMB * 1024 * 1024,
			RestartPolicy: RestartPolicy{
				Name: "no",
			},
		},
		NetworkingConfig: &NetworkingConfig{
			EndpointsConfig: map[string]EndpointSettings{
				m.cfg.DockerNetwork: {},
			},
		},
	}

	_, err := m.client.CreateContainer(ctx, name, req)
	return err
}

func (m *Manager) waitUntilReady(ctx context.Context, containerName string) error {
	deadline := time.Now().Add(m.cfg.StartTimeout)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		if m.isKasmReady(ctx, containerName) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("desktop container %s did not become ready within %s", containerName, m.cfg.StartTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) isKasmReady(ctx context.Context, containerName string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, fmt.Sprintf("http://%s:3000%s", containerName, DesktopSubfolder), nil)
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
