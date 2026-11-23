package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	ErrLockConflict = errors.New("active device lock held by another device")
	ErrLockLost     = errors.New("device lock was lost or taken over by another device")
)

type DeviceLock struct {
	Username      string    `json:"username"`
	DeviceID      string    `json:"device_id"`
	DeviceName    string    `json:"device_name"`
	OS            string    `json:"os"`
	LeaseToken    string    `json:"lease_token,omitempty"`
	Epoch         int64     `json:"epoch"`
	AcquiredAt    time.Time `json:"acquired_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type AcquireResponse struct {
	Lock        *DeviceLock `json:"lock"`
	TakenOver   bool        `json:"taken_over"`
	PreviousDev string      `json:"previous_device,omitempty"`
}

type LockConflictError struct {
	Message  string
	LockedBy *DeviceLock
}

func (e *LockConflictError) Error() string {
	if e.LockedBy != nil {
		return fmt.Sprintf("%s (holder: %s [%s], last heartbeat: %s)", e.Message, e.LockedBy.DeviceName, e.LockedBy.OS, e.LockedBy.LastHeartbeat.Format(time.RFC3339))
	}
	return e.Message
}

func (e *LockConflictError) Unwrap() error {
	return ErrLockConflict
}

type FileEntry struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ModTimeNano int64  `json:"mod_time_nano"`
	Mode        uint32 `json:"mode"`
}

type ManifestResponse struct {
	Username   string               `json:"username"`
	Files      map[string]FileEntry `json:"files"`
	ActiveLock *DeviceLock          `json:"active_lock,omitempty"`
	ServerTime int64                `json:"server_time_nano"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
	deviceID   string
	leaseToken string
}

func NewClient(serverURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(serverURL), "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) SetSession(token, deviceID, leaseToken string) {
	c.token = token
	c.deviceID = deviceID
	c.leaseToken = leaseToken
}

func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/login", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("login failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("server login response missing bearer token")
	}
	c.token = out.Token
	return out.Token, nil
}

func (c *Client) AcquireLock(ctx context.Context, deviceID, deviceName, osName string, force bool) (*AcquireResponse, error) {
	payload, _ := json.Marshal(map[string]any{
		"device_id":   deviceID,
		"device_name": deviceName,
		"os":          osName,
		"force":       force,
	})
	req, err := c.newAuthedRequest(ctx, http.MethodPost, "/api/sync/lock/acquire", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		var conflict struct {
			Error    string      `json:"error"`
			LockedBy *DeviceLock `json:"locked_by"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&conflict)
		return nil, &LockConflictError{
			Message:  conflict.Error,
			LockedBy: conflict.LockedBy,
		}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("acquire lock failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out AcquireResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	c.deviceID = deviceID
	if out.Lock != nil {
		c.leaseToken = out.Lock.LeaseToken
	}
	return &out, nil
}

func (c *Client) Heartbeat(ctx context.Context) error {
	payload, _ := json.Marshal(map[string]string{
		"device_id":   c.deviceID,
		"lease_token": c.leaseToken,
	})
	req, err := c.newAuthedRequest(ctx, http.MethodPost, "/api/sync/lock/heartbeat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

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
		return fmt.Errorf("heartbeat failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) ReleaseLock(ctx context.Context) error {
	if c.deviceID == "" || c.leaseToken == "" {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{
		"device_id":   c.deviceID,
		"lease_token": c.leaseToken,
	})
	req, err := c.newAuthedRequest(ctx, http.MethodPost, "/api/sync/lock/release", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) GetManifest(ctx context.Context) (*ManifestResponse, error) {
	req, err := c.newAuthedRequest(ctx, http.MethodGet, "/api/sync/manifest", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("get manifest failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out ManifestResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Files == nil {
		out.Files = make(map[string]FileEntry)
	}
	return &out, nil
}

func (c *Client) DownloadFile(ctx context.Context, relPath string) ([]byte, int64, os.FileMode, error) {
	endpoint := "/api/sync/file?path=" + url.QueryEscape(relPath)
	req, err := c.newAuthedRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, 0, 0, fmt.Errorf("download %s failed (%d): %s", relPath, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, 0, err
	}
	modTime, _ := strconv.ParseInt(resp.Header.Get("X-File-ModTime"), 10, 64)
	perm := os.FileMode(0o644)
	if rawMode := resp.Header.Get("X-File-Mode"); rawMode != "" {
		if parsed, err := strconv.ParseUint(rawMode, 8, 32); err == nil && parsed != 0 {
			perm = os.FileMode(parsed)
		}
	}
	return data, modTime, perm, nil
}

func (c *Client) UploadFile(ctx context.Context, relPath string, fullPath string, modTimeNano int64, perm os.FileMode) (*FileEntry, error) {
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	endpoint := "/api/sync/file?path=" + url.QueryEscape(relPath)
	req, err := c.newAuthedRequest(ctx, http.MethodPut, endpoint, f)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Device-ID", c.deviceID)
	req.Header.Set("X-Lease-Token", c.leaseToken)
	req.Header.Set("X-File-ModTime", strconv.FormatInt(modTimeNano, 10))
	req.Header.Set("X-File-Mode", strconv.FormatUint(uint64(perm.Perm()), 8))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusPreconditionFailed {
		return nil, ErrLockLost
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("upload %s failed (%d): %s", relPath, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var entry FileEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (c *Client) DeleteFile(ctx context.Context, relPath string) error {
	endpoint := "/api/sync/file?path=" + url.QueryEscape(relPath)
	req, err := c.newAuthedRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
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
		return fmt.Errorf("delete %s failed (%d): %s", relPath, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) newAuthedRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}
