package syncer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ExecJob struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	Mount      string `json:"mount,omitempty"`
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
	return c.PollExecJobWithMounts(ctx, timeoutSec, c.deviceName, nil)
}

func (c *Client) PollExecJobWithMounts(ctx context.Context, timeoutSec int, deviceName string, mounts []string) (*ExecJob, error) {
	if timeoutSec <= 0 {
		timeoutSec = 25
	}
	endpoint := fmt.Sprintf("/api/exec/poll?timeout=%d", timeoutSec)
	if len(mounts) > 0 {
		endpoint += "&mounts=" + url.QueryEscape(strings.Join(mounts, ","))
	}
	req, err := c.newAuthedRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Device-ID", c.deviceID)
	req.Header.Set("X-Lease-Token", c.leaseToken)
	if deviceName != "" {
		req.Header.Set("X-Device-Name", deviceName)
	}
	if len(mounts) > 0 {
		req.Header.Set("X-Device-Mounts", strings.Join(mounts, ","))
	}
	if len(c.payloadKey) == 32 {
		req.Header.Set(HeaderAcceptEnc, EncSchemeAES256GCM)
	}

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
	if resp.Header.Get(HeaderEnc) == EncSchemeAES256GCM {
		if len(c.payloadKey) != 32 {
			return nil, errors.New("received encrypted exec job without local AES-256 key")
		}
		var env struct {
			Enc     string `json:"enc"`
			Payload string `json:"payload"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			return nil, err
		}
		sealed, err := base64.StdEncoding.DecodeString(env.Payload)
		if err != nil {
			return nil, err
		}
		plain, err := DecryptAESGCM(c.payloadKey, sealed, []byte("exec-job"))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(plain, &job); err != nil {
			return nil, err
		}
		return &job, nil
	}

	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

func (c *Client) SubmitExecResult(ctx context.Context, res ExecResult) error {
	var bodyBytes []byte
	var encrypted bool
	if len(c.payloadKey) == 32 {
		plain, err := json.Marshal(res)
		if err != nil {
			return err
		}
		sealed, err := EncryptAESGCM(c.payloadKey, plain, []byte("exec-result"))
		if err != nil {
			return err
		}
		bodyBytes, err = json.Marshal(map[string]string{
			"enc":     EncSchemeAES256GCM,
			"payload": base64.StdEncoding.EncodeToString(sealed),
		})
		if err != nil {
			return err
		}
		encrypted = true
	} else {
		var err error
		bodyBytes, err = json.Marshal(res)
		if err != nil {
			return err
		}
	}

	req, err := c.newAuthedRequest(ctx, http.MethodPost, "/api/exec/result", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", c.deviceID)
	req.Header.Set("X-Lease-Token", c.leaseToken)
	if encrypted {
		req.Header.Set(HeaderEnc, EncSchemeAES256GCM)
	}

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
