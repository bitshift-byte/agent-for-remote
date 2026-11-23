package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port             string
	JWTSecret        string
	JWTTTL           time.Duration
	CookieSecure     bool
	Users            map[string]string
	DockerHost       string
	DockerNetwork    string
	DesktopImage     string
	DesktopMemoryMB  int64
	DesktopCPUs      float64
	DesktopShmMB     int64
	DesktopTZ        string
	DesktopDPI       string
	DesktopFontSize  string
	DesktopTheme     string
	DesktopIdlePause time.Duration
	// Performance profile: "smooth" (default, latency-first) or "quality".
	// smooth: CBR + congestion control, streaming mode, paint-over off,
	// 30fps, 1280x720 locked, striped x264 encoder, small backpressure queue.
	// quality: the old defaults (CRF, paint-over on, no streaming mode).
	PerformanceProfile           string
	SelkiesPaintOverQuality      string
	SelkiesVideoStreamingMode    bool
	SelkiesRateControlMode       string
	SelkiesVideoBitrate          string
	SelkiesFramerate             string
	SelkiesEncoder               string
	SelkiesManualWidth           int
	SelkiesManualHeight          int
	SelkiesEnableResize          bool
	SelkiesBackpressureQueueSize int
	SelkiesCongestionControl     bool
	SelkiesExtraEnv              string
	StartTimeout                 time.Duration
	MockDesktop                  bool
	WorkspaceDataDir             string
	WorkspaceHostDir             string
	SyncLockTTL                  time.Duration

	// Runtime AI provider env vars injected into user containers (never written to /workspace)
	AnthropicAPIKey         string
	AnthropicBaseURL        string
	AnthropicModel          string
	AnthropicSmallFastModel string
	OpenAIAPIKey            string
	OpenAIBaseURL           string
	CodexModel              string
}

func Load() (*Config, error) {
	port := getEnv("GATEWAY_PORT", "8080")
	jwtSecret := getEnv("JWT_SECRET", "dev-insecure-secret-change-in-production")
	ttlHours := getEnvInt("JWT_TTL_HOURS", 24)
	cookieSecure := getEnvBool("COOKIE_SECURE", false)

	users, err := parseUsers(getEnv("AUTH_USERS", "admin:admin123,demo:demo123"))
	if err != nil {
		return nil, err
	}

	dockerHost := getEnv("DOCKER_HOST", "unix:///var/run/docker.sock")
	if legacy := os.Getenv("DOCKER_SOCKET"); legacy != "" && os.Getenv("DOCKER_HOST") == "" {
		dockerHost = "unix://" + legacy
	}

	// Low-latency streaming profile. Individual SELKIES_* vars below still
	// override the profile defaults, and SELKIES_EXTRA_ENV overrides all.
	profile := strings.ToLower(strings.TrimSpace(getEnv("DESKTOP_PERFORMANCE_PROFILE", "smooth")))
	smooth := profile != "quality"
	paintOver, streamingMode, rateMode := "false", true, "cbr"
	bitrate, framerate, encoder := "2500", "30", "h264enc-striped,h264enc,jpeg"
	manualW, manualH, enableResize, backpressure := 1280, 720, false, 30
	if !smooth {
		paintOver, streamingMode, rateMode = "true", false, "crf"
		bitrate, framerate, encoder = "", "", ""
		manualW, manualH, enableResize, backpressure = 0, 0, true, 120
	}

	cfg := &Config{
		Port:                         port,
		JWTSecret:                    jwtSecret,
		JWTTTL:                       time.Duration(ttlHours) * time.Hour,
		CookieSecure:                 cookieSecure,
		Users:                        users,
		DockerHost:                   dockerHost,
		DockerNetwork:                getEnv("DOCKER_NETWORK", "super-agent-net"),
		DesktopImage:                 getEnv("DESKTOP_IMAGE", "super-agent-desktop:latest"),
		DesktopMemoryMB:              int64(getEnvInt("DESKTOP_MEMORY_MB", 6144)),
		DesktopCPUs:                  getEnvFloat("DESKTOP_CPUS", 4.0),
		DesktopShmMB:                 int64(getEnvInt("DESKTOP_SHM_MB", 2048)),
		DesktopTZ:                    getEnv("DESKTOP_TZ", "Asia/Shanghai"),
		DesktopDPI:                   strings.TrimSpace(os.Getenv("DESKTOP_DPI")),
		DesktopFontSize:              strings.TrimSpace(os.Getenv("DESKTOP_FONT_SIZE")),
		DesktopTheme:                 strings.TrimSpace(os.Getenv("DESKTOP_THEME")),
		DesktopIdlePause:             time.Duration(getEnvNonNegInt("DESKTOP_IDLE_PAUSE_MIN", 30)) * time.Minute,
		PerformanceProfile:           profile,
		SelkiesPaintOverQuality:      getEnv("SELKIES_USE_PAINT_OVER_QUALITY", paintOver),
		SelkiesVideoStreamingMode:    getEnvBool("SELKIES_VIDEO_STREAMING_MODE", streamingMode),
		SelkiesRateControlMode:       getEnv("SELKIES_RATE_CONTROL_MODE", rateMode),
		SelkiesVideoBitrate:          getEnv("SELKIES_VIDEO_BITRATE", bitrate),
		SelkiesFramerate:             getEnv("SELKIES_FRAMERATE", framerate),
		SelkiesEncoder:               getEnv("SELKIES_ENCODER", encoder),
		SelkiesManualWidth:           getEnvInt("SELKIES_MANUAL_WIDTH", manualW),
		SelkiesManualHeight:          getEnvInt("SELKIES_MANUAL_HEIGHT", manualH),
		SelkiesEnableResize:          getEnvBool("SELKIES_ENABLE_RESIZE", enableResize),
		SelkiesBackpressureQueueSize: getEnvInt("SELKIES_BACKPRESSURE_QUEUE_SIZE", backpressure),
		SelkiesCongestionControl:     getEnvBool("SELKIES_CONGESTION_CONTROL", true),
		SelkiesExtraEnv:              strings.TrimSpace(os.Getenv("SELKIES_EXTRA_ENV")),
		StartTimeout:                 time.Duration(getEnvInt("DESKTOP_START_TIMEOUT_SEC", 60)) * time.Second,
		MockDesktop:                  getEnvBool("MOCK_DESKTOP", false),
		WorkspaceDataDir:             getEnv("WORKSPACE_DATA_DIR", "/data/workspaces"),
		WorkspaceHostDir:             strings.TrimRight(strings.TrimSpace(os.Getenv("WORKSPACE_HOST_DIR")), "/"),
		SyncLockTTL:                  time.Duration(getEnvInt("SYNC_LOCK_TTL_SEC", 60)) * time.Second,
		AnthropicAPIKey:              os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicBaseURL:             os.Getenv("ANTHROPIC_BASE_URL"),
		AnthropicModel:               strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")),
		AnthropicSmallFastModel:      strings.TrimSpace(os.Getenv("ANTHROPIC_SMALL_FAST_MODEL")),
		OpenAIAPIKey:                 os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:                os.Getenv("OPENAI_BASE_URL"),
		CodexModel:                   strings.TrimSpace(os.Getenv("CODEX_MODEL")),
	}

	return cfg, nil
}

func parseUsers(raw string) (map[string]string, error) {
	users := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid AUTH_USERS entry %q (expected username:password)", entry)
		}
		users[strings.TrimSpace(parts[0])] = parts[1]
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("AUTH_USERS must contain at least one user")
	}
	return users, nil
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getEnvNonNegInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func getEnvFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return fallback
	}
	return f
}

func getEnvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
