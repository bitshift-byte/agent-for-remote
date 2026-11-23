package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"super-agent/gateway/internal/docker"
)

type VNCProxy struct {
	manager *docker.Manager
}

func NewVNCProxy(manager *docker.Manager) *VNCProxy {
	return &VNCProxy{manager: manager}
}

// ServeUser forwards both HTTP assets and WebSocket upgrades (KasmVNC websockify)
// directly to the authenticated user's isolated desktop container on the internal Docker network.
func (p *VNCProxy) ServeUser(w http.ResponseWriter, r *http.Request, username string) {
	p.manager.TouchActivity(username)

	target, err := url.Parse(p.manager.UpstreamURL(username))
	if err != nil {
		http.Error(w, "invalid upstream target", http.StatusInternalServerError)
		return
	}

	rp := &httputil.ReverseProxy{
		FlushInterval: 100 * time.Millisecond,
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host

			// Ensure /desktop redirects/normalizes cleanly to /desktop/
			if req.URL.Path == "/desktop" {
				req.URL.Path = docker.DesktopSubfolder
			}

			// Preserve Origin/Host expectations for KasmVNC WebSocket checks
			if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
				req.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			log.Printf("[proxy] user=%s path=%s err=%v", username, req.URL.Path, proxyErr)
			http.Error(rw, "Desktop workspace is not running or still starting. Please return to the portal and wake it.", http.StatusBadGateway)
		},
	}

	rp.ServeHTTP(w, r)
}
