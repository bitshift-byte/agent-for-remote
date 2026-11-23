package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"super-agent/gateway/internal/syncsvc"
	"super-agent/gateway/internal/tunnel"
)

const maxTunnelBodyBytes = 8 * 1024 * 1024

func (s *Server) registerTunnelRoutes() {
	s.mux.HandleFunc("/api/tunnel/status", s.requireAuth(s.handleTunnelStatus))
	s.mux.HandleFunc("/api/tunnel/poll", s.requireAuth(s.handleTunnelPoll))
	s.mux.HandleFunc("/api/tunnel/response", s.requireAuth(s.handleTunnelResponse))
	s.mux.HandleFunc("/api/tunnel/proxy/", s.requireAuth(s.handleTunnelProxy))
}

func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tunnel_online": s.tunnelBroker != nil && s.tunnelBroker.HasActiveWorker(username),
	})
}

func (s *Server) handleTunnelProxy(w http.ResponseWriter, r *http.Request, username string) {
	if s.tunnelBroker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tunnel broker unavailable"})
		return
	}
	s.docker.TouchActivity(username)

	trimmed := strings.TrimPrefix(r.URL.Path, "/api/tunnel/proxy/")
	parts := strings.SplitN(trimmed, "/", 2)
	port, err := strconv.Atoi(parts[0])
	if err != nil || port < 1 || port > 65535 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid target port"})
		return
	}
	targetPath := "/"
	if len(parts) == 2 && parts[1] != "" {
		targetPath = "/" + parts[1]
	}
	if r.URL.RawQuery != "" {
		targetPath += "?" + r.URL.RawQuery
	}

	var bodyB64 string
	if r.Body != nil {
		rawBody, err := io.ReadAll(io.LimitReader(r.Body, maxTunnelBodyBytes))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
			return
		}
		if len(rawBody) > 0 {
			bodyB64 = base64.StdEncoding.EncodeToString(rawBody)
		}
	}

	fwdHeaders := make(map[string][]string)
	for k, vals := range r.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "cookie" || lk == "host" || lk == "connection" || lk == "accept-encoding" {
			continue
		}
		fwdHeaders[k] = append([]string(nil), vals...)
	}

	resp, err := s.tunnelBroker.Submit(r.Context(), username, tunnel.TunnelRequest{
		Method:  r.Method,
		Port:    port,
		Path:    targetPath,
		Headers: fwdHeaders,
		BodyB64: bodyB64,
	}, 30*time.Second)
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, tunnel.ErrReqTimeout) {
			code = http.StatusGatewayTimeout
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	if resp.Error != "" && resp.Status == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": resp.Error})
		return
	}

	for k, vals := range resp.Headers {
		lk := strings.ToLower(k)
		if lk == "transfer-encoding" || lk == "connection" || lk == "content-length" {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	status := resp.Status
	if status <= 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if resp.BodyB64 != "" {
		if raw, decErr := base64.StdEncoding.DecodeString(resp.BodyB64); decErr == nil {
			_, _ = w.Write(raw)
		}
	}
}

func (s *Server) handleTunnelPoll(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.verifyWriteLock(w, r, username) {
		return
	}

	timeoutSec := 25
	if raw := r.URL.Query().Get("timeout"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 55 {
			timeoutSec = v
		}
	}
	pollCtx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	req, err := s.tunnelBroker.Poll(pollCtx, username)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if r.Header.Get(syncsvc.HeaderAcceptEnc) == syncsvc.EncSchemeAES256GCM {
		if key := s.requestPayloadKey(r); key != nil {
			plain, err := json.Marshal(req)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			sealed, err := syncsvc.EncryptAESGCM(key, plain, []byte("tunnel-req"))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set(syncsvc.HeaderEnc, syncsvc.EncSchemeAES256GCM)
			writeJSON(w, http.StatusOK, map[string]string{
				"enc":     syncsvc.EncSchemeAES256GCM,
				"payload": base64.StdEncoding.EncodeToString(sealed),
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleTunnelResponse(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.verifyWriteLock(w, r, username) {
		return
	}

	var res tunnel.TunnelResponse
	if r.Header.Get(syncsvc.HeaderEnc) == syncsvc.EncSchemeAES256GCM {
		key := s.requestPayloadKey(r)
		if key == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "encrypted response missing key"})
			return
		}
		var env struct {
			Enc     string `json:"enc"`
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTunnelBodyBytes*2)).Decode(&env); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid encrypted envelope"})
			return
		}
		sealed, err := base64.StdEncoding.DecodeString(env.Payload)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid base64 payload"})
			return
		}
		plain, err := syncsvc.DecryptAESGCM(key, sealed, []byte("tunnel-resp"))
		if err != nil {
			log.Printf("[tunnel/response] decrypt failed user=%s err=%v", username, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "decryption failed"})
			return
		}
		if err := json.Unmarshal(plain, &res); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid decrypted body"})
			return
		}
	} else {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTunnelBodyBytes*2)).Decode(&res); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid response body"})
			return
		}
	}

	if err := s.tunnelBroker.Complete(res); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
