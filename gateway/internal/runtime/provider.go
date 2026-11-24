package runtime

import (
	"context"
	"time"

	"super-agent/gateway/internal/config"
)

const DesktopSubfolder = "/desktop/"

type DesktopStatus struct {
	Username      string  `json:"username"`
	ContainerName string  `json:"container_name"`
	Runtime       string  `json:"runtime,omitempty"` // "docker" or "incus-kvm"
	State         string  `json:"state"`             // "running", "paused", "stopped", "not_created"
	Ready         bool    `json:"ready"`
	DesktopURL    string  `json:"desktop_url"`
	MemoryMB      int64   `json:"memory_mb"`
	CPUs          float64 `json:"cpus"`
}

// Provider abstracts the per-user desktop runtime backend (Docker container or Incus KVM virtual machine).
type Provider interface {
	IsMock() bool
	Config() *config.Config
	TouchActivity(username string)
	LastActive(username string) time.Time
	UpstreamURL(username string) string
	GetStatus(ctx context.Context, username string) (*DesktopStatus, error)
	EnsureRunning(ctx context.Context, username string) (*DesktopStatus, error)
	StopDesktop(ctx context.Context, username string) error
	StartIdleReaper(ctx context.Context)
}
