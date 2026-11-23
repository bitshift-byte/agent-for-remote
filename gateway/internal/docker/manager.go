package docker

import (
	"context"
	"errors"
	"fmt"
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
	client *Client
	cfg    *config.Config

	mu         sync.Mutex
	userLocks  map[string]*sync.Mutex
	lastActive map[string]time.Time
}

func NewManager(client *Client, cfg *config.Config) *Manager {
	return &Manager{
		client:     client,
		cfg:        cfg,
		userLocks:  make(map[string]*sync.Mutex),
		lastActive: make(map[string]time.Time),
	}
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

	if err := m.client.EnsureNetwork(ctx, m.cfg.DockerNetwork); err != nil {
		return nil, err
	}

	name := ContainerName(username)
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

	req := CreateContainerRequest{
		Image: m.cfg.DesktopImage,
		Env:   env,
		Labels: map[string]string{
			"super-agent.managed": "true",
			"super-agent.user":    safeUser,
		},
		HostConfig: HostConfig{
			// Separate persistent volumes:
			// - sa-workspace-<user>: project code (/workspace)
			// - sa-config-<user>: browser cookies, ~/.claude, ~/.codex, XFCE & IME config (/config)
			Binds: []string{
				fmt.Sprintf("sa-workspace-%s:/workspace", safeUser),
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
