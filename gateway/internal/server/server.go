package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"super-agent/gateway/internal/auth"
	"super-agent/gateway/internal/config"
	"super-agent/gateway/internal/docker"
	"super-agent/gateway/internal/execbridge"
	"super-agent/gateway/internal/proxy"
	"super-agent/gateway/internal/syncsvc"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	cfg        *config.Config
	authMgr    *auth.Manager
	docker     *docker.Manager
	proxy      *proxy.VNCProxy
	syncSvc    *syncsvc.Service
	execBroker *execbridge.Broker
	mux        *http.ServeMux
}

func New(cfg *config.Config, authMgr *auth.Manager, dockerMgr *docker.Manager, vncProxy *proxy.VNCProxy, syncSvc *syncsvc.Service, execBroker *execbridge.Broker) *Server {
	s := &Server{
		cfg:        cfg,
		authMgr:    authMgr,
		docker:     dockerMgr,
		proxy:      vncProxy,
		syncSvc:    syncSvc,
		execBroker: execBroker,
		mux:        http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("failed to load embedded web assets: %v", err)
	}
	fileServer := http.FileServer(http.FS(webSub))

	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/api/me", s.requireAuth(s.handleMe))
	s.mux.HandleFunc("/api/desktop/status", s.requireAuth(s.handleDesktopStatus))
	s.mux.HandleFunc("/api/desktop/start", s.requireAuth(s.handleDesktopStart))
	s.mux.HandleFunc("/api/desktop/stop", s.requireAuth(s.handleDesktopStop))
	s.registerSyncRoutes()
	s.registerExecRoutes()

	// KasmVNC reverse proxy (all traffic authenticated by HttpOnly JWT cookie)
	s.mux.HandleFunc("/desktop", s.handleDesktopRedirect)
	s.mux.HandleFunc("/desktop/", s.requireAuth(s.handleDesktopProxy))

	// Portal SPA
	s.mux.Handle("/", fileServer)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	username := strings.TrimSpace(req.Username)
	expectedPass, ok := s.cfg.Users[username]
	if !ok || !auth.ConstantTimePasswordEqual(expectedPass, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
		return
	}

	token, err := s.authMgr.Sign(username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to issue token"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.JWTTTL.Seconds()),
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"username": username,
		"token":    token,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, username string) {
	writeJSON(w, http.StatusOK, map[string]string{
		"username": username,
	})
}

func (s *Server) handleDesktopStatus(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	status, err := s.docker.GetStatus(r.Context(), username)
	if err != nil {
		log.Printf("[status] user=%s err=%v", username, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleDesktopStart(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	log.Printf("[start] waking desktop container for user=%s", username)
	status, err := s.docker.EnsureRunning(r.Context(), username)
	if err != nil {
		log.Printf("[start] user=%s err=%v", username, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleDesktopStop(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	log.Printf("[stop] stopping desktop container for user=%s", username)
	if err := s.docker.StopDesktop(r.Context(), username); err != nil {
		log.Printf("[stop] user=%s err=%v", username, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) handleDesktopRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, docker.DesktopSubfolder, http.StatusFound)
}

func (s *Server) handleDesktopProxy(w http.ResponseWriter, r *http.Request, username string) {
	s.proxy.ServeUser(w, r, username)
}

type authedHandler func(w http.ResponseWriter, r *http.Request, username string)

func (s *Server) requireAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		claims, err := s.authMgr.Verify(token)
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
				return
			}
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		next(w, r, claims.Sub)
	}
}

func extractToken(r *http.Request) string {
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
		return c.Value
	}
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}
