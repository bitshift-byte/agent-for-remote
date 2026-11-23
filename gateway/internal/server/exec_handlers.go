package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"super-agent/gateway/internal/execbridge"
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
		"worker_online": s.execBroker.HasActiveWorker(username),
		"active_lock":   s.syncSvc.Locks().Get(username),
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

	log.Printf("[exec/run] user=%s cwd=%s cmd=%q", username, req.Cwd, truncateForLog(req.Command, 120))
	res, err := s.execBroker.Submit(r.Context(), username, req.Command, req.Cwd, req.TimeoutSec)
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, execbridge.ErrJobTimeout) {
			code = http.StatusGatewayTimeout
		}
		log.Printf("[exec/run] FAILED user=%s err=%v", username, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[exec/run] DONE user=%s job=%s exit=%d duration=%dms pre_sync=%d post_sync=%d",
		username, res.JobID, res.ExitCode, res.DurationMs, res.PreSynced, res.PostSynced)
	writeJSON(w, http.StatusOK, res)
}

// handleExecPoll is called by the local sync-agent holding the active device lock.
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

	pollCtx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	job, err := s.execBroker.Poll(pollCtx, username)
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&res); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid result body"})
		return
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
