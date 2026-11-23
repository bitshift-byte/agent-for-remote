package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"super-agent/gateway/internal/syncsvc"
)

func (s *Server) registerPairingRoutes() {
	s.mux.HandleFunc("/api/devices", s.requireAuth(s.handleDevices))
	s.mux.HandleFunc("/api/devices/pair/create", s.requireAuth(s.handlePairCreate))
	// Public endpoint: new tray client exchanges a 6-char pairing code for a dedicated DeviceToken
	s.mux.HandleFunc("/api/devices/pair/claim", s.handlePairClaim)
}

func (s *Server) handlePairCreate(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	pc, err := s.devices.CreatePairingCode(username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	serverURL := scheme + "://" + r.Host
	bindStr := syncsvc.FormatBindString(serverURL, pc.Code)

	log.Printf("[pair] created code=%s user=%s expires=%s", pc.Code, username, pc.ExpiresAt.Format("15:04:05"))
	writeJSON(w, http.StatusOK, map[string]any{
		"code":        pc.Code,
		"expires_at":  pc.ExpiresAt,
		"bind_string": bindStr,
	})
}

type pairClaimRequest struct {
	Code       string `json:"code"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	OS         string `json:"os"`
}

func (s *Server) handlePairClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req pairClaimRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	dev, err := s.devices.ClaimPairingCode(req.Code, req.DeviceID, req.DeviceName, req.OS)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, syncsvc.ErrInvalidPairCode) {
			code = http.StatusUnauthorized
		}
		log.Printf("[pair] CLAIM FAILED device=%s(%s) err=%v", req.DeviceName, req.DeviceID, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[pair] BOUND user=%s device=%s(%s) os=%s", dev.Username, dev.DeviceName, dev.DeviceID, dev.OS)
	writeJSON(w, http.StatusOK, dev)
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request, username string) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"devices":     s.devices.ListByUser(username),
			"active_lock": s.syncSvc.Locks().Get(username),
		})
	case http.MethodDelete:
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
		if deviceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing device_id"})
			return
		}
		if err := s.devices.Revoke(username, deviceID); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		// Force-release active lock if the revoked device currently holds it
		if cur := s.syncSvc.Locks().Get(username); cur != nil && cur.DeviceID == deviceID {
			s.syncSvc.Locks().ForceReleaseDevice(username, deviceID)
		}
		log.Printf("[pair] REVOKED user=%s device=%s", username, deviceID)
		writeJSON(w, http.StatusOK, map[string]string{"revoked": deviceID})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
