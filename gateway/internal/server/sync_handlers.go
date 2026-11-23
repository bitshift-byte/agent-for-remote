package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"super-agent/gateway/internal/syncsvc"
)

func (s *Server) registerSyncRoutes() {
	s.mux.HandleFunc("/api/sync/lock", s.requireAuth(s.handleSyncLockStatus))
	s.mux.HandleFunc("/api/sync/lock/acquire", s.requireAuth(s.handleSyncLockAcquire))
	s.mux.HandleFunc("/api/sync/lock/heartbeat", s.requireAuth(s.handleSyncLockHeartbeat))
	s.mux.HandleFunc("/api/sync/lock/release", s.requireAuth(s.handleSyncLockRelease))
	s.mux.HandleFunc("/api/sync/manifest", s.requireAuth(s.handleSyncManifest))
	s.mux.HandleFunc("/api/sync/file", s.requireAuth(s.handleSyncFile))
	s.mux.HandleFunc("/api/sync/dir", s.requireAuth(s.handleSyncDir))
}

func (s *Server) handleSyncLockStatus(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	lock := s.syncSvc.Locks().Get(username)
	writeJSON(w, http.StatusOK, map[string]any{
		"locked": lock != nil,
		"lock":   lock,
	})
}

func (s *Server) handleSyncLockAcquire(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req syncsvc.AcquireRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_id is required"})
		return
	}

	res, holder, err := s.syncSvc.Locks().Acquire(username, req)
	if err != nil {
		if errors.Is(err, syncsvc.ErrLockConflict) {
			log.Printf("[sync/lock] CONFLICT user=%s requester=%s(%s) holder=%s", username, req.DeviceName, req.DeviceID, holder.DeviceName)
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     err.Error(),
				"locked_by": holder,
			})
			return
		}
		log.Printf("[sync/lock] acquire error user=%s err=%v", username, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[sync/lock] ACQUIRED user=%s device=%s(%s) os=%s epoch=%d taken_over=%v prev=%s",
		username, res.Lock.DeviceName, res.Lock.DeviceID, res.Lock.OS, res.Lock.Epoch, res.TakenOver, res.PreviousDev)
	writeJSON(w, http.StatusOK, res)
}

type leaseRequest struct {
	DeviceID   string `json:"device_id"`
	LeaseToken string `json:"lease_token"`
}

func (s *Server) handleSyncLockHeartbeat(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req leaseRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	lock, err := s.syncSvc.Locks().Heartbeat(username, req.DeviceID, req.LeaseToken)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, syncsvc.ErrLockInvalid) {
			code = http.StatusPreconditionFailed
		}
		log.Printf("[sync/lock] HEARTBEAT REJECTED user=%s device=%s err=%v", username, req.DeviceID, err)
		writeJSON(w, code, map[string]any{
			"error":     err.Error(),
			"locked_by": s.syncSvc.Locks().Get(username),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lock": lock})
}

func (s *Server) handleSyncLockRelease(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req leaseRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	released := s.syncSvc.Locks().Release(username, req.DeviceID, req.LeaseToken)
	log.Printf("[sync/lock] RELEASE user=%s device=%s released=%v", username, req.DeviceID, released)
	writeJSON(w, http.StatusOK, map[string]bool{"released": released})
}

func (s *Server) handleSyncManifest(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	manifest, err := s.syncSvc.BuildManifest(username)
	if err != nil {
		log.Printf("[sync/manifest] error user=%s err=%v", username, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

func (s *Server) handleSyncFile(w http.ResponseWriter, r *http.Request, username string) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path query parameter"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		f, entry, err := s.syncSvc.OpenFile(username, relPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
				return
			}
			log.Printf("[sync/file] GET error user=%s path=%s err=%v", username, relPath, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-File-SHA256", entry.SHA256)
		w.Header().Set("X-File-ModTime", strconv.FormatInt(entry.ModTimeNano, 10))
		w.Header().Set("X-File-Mode", strconv.FormatUint(uint64(entry.Mode), 8))

		if r.Header.Get(syncsvc.HeaderAcceptEnc) == syncsvc.EncSchemeAES256GCM {
			if key := s.requestPayloadKey(r); key != nil {
				plain, readErr := io.ReadAll(io.LimitReader(f, syncsvc.MaxSyncFileSize+1))
				if readErr != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": readErr.Error()})
					return
				}
				sealed, encErr := syncsvc.EncryptAESGCM(key, plain, []byte(entry.Path))
				if encErr != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": encErr.Error()})
					return
				}
				w.Header().Set(syncsvc.HeaderEnc, syncsvc.EncSchemeAES256GCM)
				log.Printf("[sync/file] 🔒 ↓ GET user=%s path=%s size=%d (AES-256-GCM)", username, entry.Path, entry.Size)
				_, _ = w.Write(sealed)
				return
			}
		}

		log.Printf("[sync/file] ↓ GET user=%s path=%s size=%d", username, entry.Path, entry.Size)
		_, _ = io.Copy(w, f)

	case http.MethodPut:
		if !s.verifyWriteLock(w, r, username) {
			return
		}
		var modTimeNano int64
		if raw := r.Header.Get("X-File-ModTime"); raw != "" {
			modTimeNano, _ = strconv.ParseInt(raw, 10, 64)
		}
		perm := os.FileMode(0o644)
		if rawMode := r.Header.Get("X-File-Mode"); rawMode != "" {
			if parsed, err := strconv.ParseUint(rawMode, 8, 32); err == nil && parsed != 0 {
				perm = os.FileMode(parsed)
			}
		}

		var bodyReader io.Reader = r.Body
		encrypted := false
		if r.Header.Get(syncsvc.HeaderEnc) == syncsvc.EncSchemeAES256GCM {
			key := s.requestPayloadKey(r)
			if key == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "encrypted payload sent without bound device key"})
				return
			}
			normPath, normErr := syncsvc.NormalizeRelPath(relPath)
			if normErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": normErr.Error()})
				return
			}
			sealed, readErr := io.ReadAll(io.LimitReader(r.Body, syncsvc.MaxSyncFileSize+64))
			if readErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": readErr.Error()})
				return
			}
			plain, decErr := syncsvc.DecryptAESGCM(key, sealed, []byte(normPath))
			if decErr != nil {
				log.Printf("[sync/file] ⛔ PUT decrypt failed user=%s path=%s err=%v", username, normPath, decErr)
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload decryption failed"})
				return
			}
			bodyReader = strings.NewReader(string(plain))
			encrypted = true
		}

		entry, err := s.syncSvc.WriteFile(username, relPath, bodyReader, modTimeNano, perm)
		if err != nil {
			log.Printf("[sync/file] PUT error user=%s path=%s err=%v", username, relPath, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if encrypted {
			log.Printf("[sync/file] 🔒 ↑ PUT user=%s device=%s path=%s size=%d (AES-256-GCM)", username, r.Header.Get("X-Device-ID"), entry.Path, entry.Size)
		} else {
			log.Printf("[sync/file] ↑ PUT user=%s device=%s path=%s size=%d", username, r.Header.Get("X-Device-ID"), entry.Path, entry.Size)
		}
		writeJSON(w, http.StatusOK, entry)

	case http.MethodDelete:
		if !s.verifyWriteLock(w, r, username) {
			return
		}
		if err := s.syncSvc.DeleteFile(username, relPath); err != nil {
			log.Printf("[sync/file] DELETE error user=%s path=%s err=%v", username, relPath, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("[sync/file] 🗑 DELETE user=%s device=%s path=%s", username, r.Header.Get("X-Device-ID"), relPath)
		writeJSON(w, http.StatusOK, map[string]string{"deleted": relPath})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleSyncDir(w http.ResponseWriter, r *http.Request, username string) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path query parameter"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		if !s.verifyWriteLock(w, r, username) {
			return
		}
		if err := s.syncSvc.EnsureDir(username, relPath); err != nil {
			log.Printf("[sync/dir] MKDIR error user=%s path=%s err=%v", username, relPath, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("[sync/dir] ↑ MKDIR user=%s device=%s path=%s", username, r.Header.Get("X-Device-ID"), relPath)
		writeJSON(w, http.StatusOK, map[string]string{"created": relPath})

	case http.MethodDelete:
		if !s.verifyWriteLock(w, r, username) {
			return
		}
		if err := s.syncSvc.DeleteDir(username, relPath); err != nil {
			log.Printf("[sync/dir] RMDIR error user=%s path=%s err=%v", username, relPath, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("[sync/dir] 🗑 RMDIR user=%s device=%s path=%s", username, r.Header.Get("X-Device-ID"), relPath)
		writeJSON(w, http.StatusOK, map[string]string{"deleted": relPath})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) verifyWriteLock(w http.ResponseWriter, r *http.Request, username string) bool {
	deviceID := strings.TrimSpace(r.Header.Get("X-Device-ID"))
	leaseToken := strings.TrimSpace(r.Header.Get("X-Lease-Token"))
	if deviceID == "" || leaseToken == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]string{"error": "missing X-Device-ID or X-Lease-Token"})
		return false
	}
	if err := s.syncSvc.Locks().VerifyWrite(username, deviceID, leaseToken); err != nil {
		code := http.StatusConflict
		if errors.Is(err, syncsvc.ErrLockInvalid) {
			code = http.StatusPreconditionFailed
		}
		writeJSON(w, code, map[string]any{
			"error":     err.Error(),
			"locked_by": s.syncSvc.Locks().Get(username),
		})
		return false
	}
	return true
}
