package proxy

import (
	"fmt"
	"html"
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

	if p.manager.IsMock() {
		p.serveMockDesktop(w, username)
		return
	}

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

func (p *VNCProxy) serveMockDesktop(w http.ResponseWriter, username string) {
	safeUser := html.EscapeString(username)
	containerName := html.EscapeString(docker.ContainerName(username))
	sanitized := html.EscapeString(docker.SanitizeUsername(username))
	cfg := p.manager.Config()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<title>Super-Agent 本地调试预览 (%s)</title>
<style>
  body { margin:0; height:100vh; background:linear-gradient(135deg,#1d2b53 0%%,#0f172a 100%%); color:#e2e8f0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; display:flex; flex-direction:column; }
  .desktop-area { flex:1; padding:28px; display:flex; gap:28px; align-items:flex-start; flex-wrap:wrap; }
  .icons { display:flex; flex-direction:column; gap:18px; width:110px; }
  .icon { text-align:center; font-size:12px; background:rgba(255,255,255,0.06); border:1px solid rgba(255,255,255,0.12); border-radius:12px; padding:12px 8px; }
  .icon .glyph { font-size:24px; margin-bottom:6px; }
  .window { flex:1; min-width:340px; max-width:720px; background:#0b1020; border:1px solid #334155; border-radius:12px; overflow:hidden; box-shadow:0 24px 60px rgba(0,0,0,0.45); }
  .win-bar { background:#1e293b; padding:10px 14px; font-size:13px; display:flex; justify-content:space-between; align-items:center; }
  .win-body { padding:18px; font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:13px; line-height:1.7; color:#cbd5e1; }
  .tag { display:inline-block; background:rgba(245,158,11,0.18); color:#fcd34d; padding:2px 8px; border-radius:999px; font-size:11px; }
  .ok { color:#86efac; }
  .taskbar { height:38px; background:#090d1a; border-top:1px solid #1e293b; display:flex; align-items:center; justify-content:space-between; padding:0 16px; font-size:12px; color:#94a3b8; }
</style>
</head>
<body>
  <div class="desktop-area">
    <div class="icons">
      <div class="icon"><div class="glyph">🤖</div>Claude Code</div>
      <div class="icon"><div class="glyph">⚡</div>OpenAI Codex</div>
      <div class="icon"><div class="glyph">🌐</div>Chrome</div>
      <div class="icon"><div class="glyph">💻</div>Terminal</div>
    </div>
    <div class="window">
      <div class="win-bar">
        <span>Ubuntu XFCE + KasmVNC 预览窗口</span>
        <span class="tag">MOCK_DESKTOP 本地调试模式</span>
      </div>
      <div class="win-body">
        <div class="ok">✔ JWT 鉴权、门户状态机与 /desktop/ 反向代理路由已跑通</div>
        <div>• 当前用户: <strong>%s</strong></div>
        <div>• 目标容器: <strong>%s</strong> (image: %s)</div>
        <div>• 资源配额: %.1f vCPU / %d MB RAM / %d MB /dev/shm</div>
        <div>• 持久化卷: sa-workspace-%s → /workspace, sa-config-%s → /config</div>
        <hr style="border:none;border-top:1px solid #1e293b;margin:14px 0;" />
        <div>说明：当前本机未检测到 Docker 运行时，已启用 <code>MOCK_DESKTOP=true</code> 预览模式。</div>
        <div>在装有 Docker 的机器或海外服务器上启动（默认 <code>MOCK_DESKTOP=false</code>）后，此处将直接渲染真实 KasmVNC Ubuntu 桌面。</div>
      </div>
    </div>
  </div>
  <div class="taskbar">
    <span>Applications · /workspace</span>
    <span>fcitx5 (中) · %s</span>
  </div>
</body>
</html>`, safeUser, safeUser, containerName, html.EscapeString(cfg.DesktopImage), cfg.DesktopCPUs, cfg.DesktopMemoryMB, cfg.DesktopShmMB, sanitized, sanitized, html.EscapeString(cfg.DesktopTZ))
}
