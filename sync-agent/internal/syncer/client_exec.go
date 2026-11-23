package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ExecJob struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	TimeoutSec int    `json:"timeout_sec"`
}

type ExecResult struct {
	JobID      string `json:"job_id"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	PreSynced  int    `json:"pre_synced"`
	PostSynced int    `json:"post_synced"`
	Error      string `json:"error,omitempty"`
}

func (c *Client) PollExecJob(ctx context.Context, timeoutSec int) (*ExecJob, error) {
	if timeoutSec <= 0 {
		timeoutSec = 25
	}
	endpoint := fmt.Sprintf("/api/exec/poll?timeout=%d", timeoutSec)
	req, err := c.newAuthedRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Device-ID", c.deviceID)
	req.Header.Set("X-Lease-Token", c.leaseToken)

	pollClient := &http.Client{
		Transport: c.httpClient.Transport,
		Timeout:   time.Duration(timeoutSec+10) * time.Second,
	}
	resp, err := pollClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusPreconditionFailed {
		return nil, ErrLockLost
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("poll exec failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var job ExecJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

func (c *Client) SubmitExecResult(ctx context.Context, res ExecResult) error {
	payload, err := json.Marshal(res)
	if err != nil {
		return err
	}
	req, err := c.newAuthedRequest(ctx, http.MethodPost, "/api/exec/result", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", c.deviceID)
	req.Header.Set("X-Lease-Token", c.leaseToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusPreconditionFailed {
		return ErrLockLost
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("submit exec result failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
