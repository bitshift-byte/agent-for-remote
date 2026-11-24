package docker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"super-agent/gateway/internal/config"
	"super-agent/gateway/internal/runtime"
)

const DesktopSubfolder = runtime.DesktopSubfolder

var validNameRe = regexp.MustCompile(`[^a-z0-9_-]+`)

type DesktopStatus = runtime.DesktopStatus

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
		Runtime:       "docker",
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

// StartIdleReaper periodically pauses running desktop containers that have been idle longer than DesktopIdlePause,
// reducing idle CPU to zero while preserving all open GUI apps & terminal sessions for instant Unpause wake-up.
func (m *Manager) StartIdleReaper(ctx context.Context) {
	if m.cfg.DesktopIdlePause <= 0 {
		log.Println("[reaper] idle auto-pause disabled (DESKTOP_IDLE_PAUSE_MIN=0)")
		return
	}
	log.Printf("[reaper] idle auto-pause enabled: pausing containers after %s of inactivity", m.cfg.DesktopIdlePause)
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
		if m.cfg.MockDesktop {
			m.mu.Lock()
			if m.mockStates[user] == "running" {
				m.mockStates[user] = "paused"
				log.Printf("[reaper] paused mock desktop for user=%s after %s idle", user, m.cfg.DesktopIdlePause)
			}
			m.mu.Unlock()
			ulock.Unlock()
			continue
		}
		name := ContainerName(user)
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		info, err := m.client.InspectContainer(checkCtx, name)
		if err == nil && info.State.Running && !info.State.Paused {
			if pauseErr := m.client.PauseContainer(checkCtx, name); pauseErr != nil {
				log.Printf("[reaper] pause %s failed: %v", name, pauseErr)
			} else {
				log.Printf("[reaper] ❄ auto-paused idle desktop %s (user=%s, idle=%s)", name, user, time.Since(last).Round(time.Second))
			}
		}
		cancel()
		ulock.Unlock()
	}
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
	if m.cfg.AnthropicModel != "" {
		env = append(env, "ANTHROPIC_MODEL="+m.cfg.AnthropicModel)
	}
	if m.cfg.AnthropicSmallFastModel != "" {
		env = append(env, "ANTHROPIC_SMALL_FAST_MODEL="+m.cfg.AnthropicSmallFastModel)
	}
	if m.cfg.OpenAIAPIKey != "" {
		env = append(env, "OPENAI_API_KEY="+m.cfg.OpenAIAPIKey)
	}
	if m.cfg.OpenAIBaseURL != "" {
		env = append(env, "OPENAI_BASE_URL="+m.cfg.OpenAIBaseURL)
	}
	if m.cfg.CodexModel != "" {
		env = append(env, "CODEX_MODEL="+m.cfg.CodexModel)
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
	// Low-latency streaming env. SELKIES_EXTRA_ENV pairs override these
	// (applied last, keyed by name, so no duplicate keys reach Docker).
	selkiesEnv := map[string]string{
		"SELKIES_USE_PAINT_OVER_QUALITY":  m.cfg.SelkiesPaintOverQuality,
		"SELKIES_VIDEO_STREAMING_MODE":    strconv.FormatBool(m.cfg.SelkiesVideoStreamingMode),
		"SELKIES_RATE_CONTROL_MODE":       m.cfg.SelkiesRateControlMode,
		"SELKIES_VIDEO_BITRATE":           m.cfg.SelkiesVideoBitrate,
		"SELKIES_FRAMERATE":               m.cfg.SelkiesFramerate,
		"SELKIES_ENCODER":                 m.cfg.SelkiesEncoder,
		"SELKIES_ENABLE_RESIZE":           strconv.FormatBool(m.cfg.SelkiesEnableResize),
		"SELKIES_BACKPRESSURE_QUEUE_SIZE": strconv.Itoa(m.cfg.SelkiesBackpressureQueueSize),
		"SELKIES_CONGESTION_CONTROL":      strconv.FormatBool(m.cfg.SelkiesCongestionControl),
	}
	if m.cfg.SelkiesManualWidth > 0 {
		selkiesEnv["SELKIES_MANUAL_WIDTH"] = strconv.Itoa(m.cfg.SelkiesManualWidth)
	}
	if m.cfg.SelkiesManualHeight > 0 {
		selkiesEnv["SELKIES_MANUAL_HEIGHT"] = strconv.Itoa(m.cfg.SelkiesManualHeight)
	}
	if m.cfg.SelkiesExtraEnv != "" {
		for _, pair := range strings.Split(m.cfg.SelkiesExtraEnv, ",") {
			pair = strings.TrimSpace(pair)
			if k, v, ok := strings.Cut(pair, "="); ok && strings.TrimSpace(k) != "" {
				selkiesEnv[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	for _, k := range []string{
		"SELKIES_USE_PAINT_OVER_QUALITY", "SELKIES_VIDEO_STREAMING_MODE",
		"SELKIES_RATE_CONTROL_MODE", "SELKIES_VIDEO_BITRATE", "SELKIES_FRAMERATE",
		"SELKIES_ENCODER", "SELKIES_MANUAL_WIDTH", "SELKIES_MANUAL_HEIGHT",
		"SELKIES_ENABLE_RESIZE", "SELKIES_BACKPRESSURE_QUEUE_SIZE",
		"SELKIES_CONGESTION_CONTROL",
	} {
		if v, ok := selkiesEnv[k]; ok && v != "" {
			env = append(env, k+"="+v)
		}
	}
	// Any other SELKIES_EXTRA_ENV keys not covered above.
	for k, v := range selkiesEnv {
		switch k {
		case "SELKIES_USE_PAINT_OVER_QUALITY", "SELKIES_VIDEO_STREAMING_MODE",
			"SELKIES_RATE_CONTROL_MODE", "SELKIES_VIDEO_BITRATE", "SELKIES_FRAMERATE",
			"SELKIES_ENCODER", "SELKIES_MANUAL_WIDTH", "SELKIES_MANUAL_HEIGHT",
			"SELKIES_ENABLE_RESIZE", "SELKIES_BACKPRESSURE_QUEUE_SIZE",
			"SELKIES_CONGESTION_CONTROL":
		default:
			if v != "" {
				env = append(env, k+"="+v)
			}
		}
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
