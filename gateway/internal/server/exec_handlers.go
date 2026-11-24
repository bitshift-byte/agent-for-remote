package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"super-agent/gateway/internal/execbridge"
	"super-agent/gateway/internal/syncsvc"
)

func (s *Server) registerExecRoutes() {
	s.mux.HandleFunc("/api/exec/status", s.requireAuth(s.handleExecStatus))
	s.mux.HandleFunc("/api/exec/run", s.requireAuth(s.handleExecRun))
	s.mux.HandleFunc("/api/exec/poll", s.requireAuth(s.handleExecPoll))
	s.mux.HandleFunc("/api/exec/result", s.requireAuth(s.handleExecResult))
}

func (s *Server) handleExecStatus(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"worker_online":  s.execBroker.HasActiveWorker(username),
		"active_lock":    s.syncSvc.Locks().Get(username),
		"active_locks":   s.syncSvc.Locks().ListUserLocks(username),
		"online_devices": s.execBroker.OnlineDevices(username),
	})
}

type execRunRequest struct {
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	TimeoutSec int    `json:"timeout_sec"`
}

// handleExecRun is invoked by the cloud container's transparent shell wrapper (sa-local-sh).
func (s *Server) handleExecRun(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req execRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}
	s.docker.TouchActivity(username)

	log.Printf("[exec/run] user=%s cwd=%s cmd=%q", username, req.Cwd, truncateForLog(req.Command, 120))
	res, err := s.execBroker.Submit(r.Context(), username, req.Command, req.Cwd, req.TimeoutSec)
	if err != nil {
		code := http.StatusServiceUnavailable
		errCode := "exec_unavailable"
		if errors.Is(err, execbridge.ErrNoActiveWorker) {
			errCode = "worker_offline"
		} else if errors.Is(err, execbridge.ErrJobTimeout) {
			code = http.StatusGatewayTimeout
			errCode = "job_timeout"
		}
		log.Printf("[exec/run] FAILED user=%s code=%s err=%v", username, errCode, err)
		writeJSON(w, code, map[string]string{
			"error": err.Error(),
			"code":  errCode,
		})
		return
	}

	log.Printf("[exec/run] DONE user=%s job=%s exit=%d duration=%dms pre_sync=%d post_sync=%d",
		username, res.JobID, res.ExitCode, res.DurationMs, res.PreSynced, res.PostSynced)
	writeJSON(w, http.StatusOK, res)
}

// handleExecPoll is called by each connected local sync-agent holding one or more active device leases.
func (s *Server) handleExecPoll(w http.ResponseWriter, r *http.Request, username string) {
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

	deviceID := strings.TrimSpace(r.Header.Get("X-Device-ID"))
	deviceName := strings.TrimSpace(r.Header.Get("X-Device-Name"))
	rawMounts := strings.TrimSpace(r.URL.Query().Get("mounts"))
	if rawMounts == "" {
		rawMounts = strings.TrimSpace(r.Header.Get("X-Device-Mounts"))
	}
	var mounts []string
	if rawMounts != "" {
		for _, part := range strings.Split(rawMounts, ",") {
			if norm, err := syncsvc.NormalizeMountPath(part); err == nil && norm != "" {
				mounts = append(mounts, norm)
			}
		}
	}

	pollCtx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	job, err := s.execBroker.PollDevice(pollCtx, username, deviceID, deviceName, mounts)
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
			plain, err := json.Marshal(job)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			sealed, err := syncsvc.EncryptAESGCM(key, plain, []byte("exec-job"))
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

	writeJSON(w, http.StatusOK, job)
}

// handleExecResult receives the command output from the local sync-agent.
func (s *Server) handleExecResult(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !s.verifyWriteLock(w, r, username) {
		return
	}

	var res execbridge.ExecResult
	if r.Header.Get(syncsvc.HeaderEnc) == syncsvc.EncSchemeAES256GCM {
		key := s.requestPayloadKey(r)
		if key == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "encrypted exec result sent without bound device key"})
			return
		}
		var env struct {
			Enc     string `json:"enc"`
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&env); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid encrypted envelope"})
			return
		}
		sealed, err := base64.StdEncoding.DecodeString(env.Payload)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid base64 payload"})
			return
		}
		plain, err := syncsvc.DecryptAESGCM(key, sealed, []byte("exec-result"))
		if err != nil {
			log.Printf("[exec/result] ⛔ decrypt failed user=%s err=%v", username, err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exec result decryption failed"})
			return
		}
		if err := json.Unmarshal(plain, &res); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid decrypted result body"})
			return
		}
	} else {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&res); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid result body"})
			return
		}
	}

	if err := s.execBroker.Complete(res); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
