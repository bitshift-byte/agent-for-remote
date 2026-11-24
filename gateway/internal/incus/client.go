package incus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrNotFound = errors.New("incus instance not found")

type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient connects to the Incus daemon over its local Unix socket (default: /var/lib/incus/unix.socket)
// using only Go's standard library (zero external dependencies).
func NewClient(socketPath string) *Client {
	socketPath = strings.TrimSpace(socketPath)
	socketPath = strings.TrimPrefix(socketPath, "unix://")
	if socketPath == "" {
		socketPath = "/var/lib/incus/unix.socket"
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		},
		baseURL: "http://unix.socket",
	}
}

type apiEnvelope struct {
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	StatusCode int             `json:"status_code"`
	Operation  string          `json:"operation"`
	ErrorCode  int             `json:"error_code"`
	Error      string          `json:"error"`
	Metadata   json.RawMessage `json:"metadata"`
}

type InstanceState struct {
	Status     string                     `json:"status"` // "Running", "Stopped", "Frozen"
	StatusCode int                        `json:"status_code"`
	Network    map[string]NetworkCounters `json:"network"`
}

type NetworkCounters struct {
	Addresses []NetworkAddress `json:"addresses"`
}

type NetworkAddress struct {
	Family  string `json:"family"`  // "inet" or "inet6"
	Address string `json:"address"` // IPv4/IPv6
	Scope   string `json:"scope"`   // "global", "link", "local"
}

type CreateInstanceRequest struct {
	Name    string                       `json:"name"`
	Type    string                       `json:"type"` // "virtual-machine"
	Config  map[string]string            `json:"config,omitempty"`
	Devices map[string]map[string]string `json:"devices,omitempty"`
	Source  InstanceSource               `json:"source"`
}

type InstanceSource struct {
	Type  string `json:"type"`  // "image"
	Alias string `json:"alias"` // e.g. "super-agent-vm-base"
}

func (c *Client) Ping(ctx context.Context) error {
	env, code, err := c.do(ctx, http.MethodGet, "/1.0", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK || env.Error != "" {
		return fmt.Errorf("incus ping failed (%d): %s", code, env.Error)
	}
	return nil
}

func (c *Client) GetInstanceState(ctx context.Context, name string) (*InstanceState, error) {
	env, code, err := c.do(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(name)+"/state", nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound || env.ErrorCode == 404 {
		return nil, ErrNotFound
	}
	if code != http.StatusOK || env.Error != "" {
		return nil, fmt.Errorf("get instance state %q failed (%d): %s", name, code, env.Error)
	}
	var st InstanceState
	if err := json.Unmarshal(env.Metadata, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (c *Client) CreateVM(ctx context.Context, req CreateInstanceRequest) error {
	req.Type = "virtual-machine"
	env, code, err := c.do(ctx, http.MethodPost, "/1.0/instances", req)
	if err != nil {
		return err
	}
	if code >= 400 || env.Error != "" {
		return fmt.Errorf("create VM %q failed (%d): %s", req.Name, code, env.Error)
	}
	if env.Type == "async" && env.Operation != "" {
		return c.waitOperation(ctx, env.Operation)
	}
	return nil
}

func (c *Client) ChangeState(ctx context.Context, name, action string, timeoutSec int, force bool) error {
	payload := map[string]any{
		"action":  action, // "start", "stop", "freeze", "unfreeze"
		"timeout": timeoutSec,
		"force":   force,
	}
	env, code, err := c.do(ctx, http.MethodPut, "/1.0/instances/"+url.PathEscape(name)+"/state", payload)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound || env.ErrorCode == 404 {
		return ErrNotFound
	}
	if code >= 400 || env.Error != "" {
		return fmt.Errorf("change VM %q state (%s) failed (%d): %s", name, action, code, env.Error)
	}
	if env.Type == "async" && env.Operation != "" {
		return c.waitOperation(ctx, env.Operation)
	}
	return nil
}

func (c *Client) waitOperation(ctx context.Context, opPath string) error {
	waitPath := opPath + "/wait?timeout=120"
	env, code, err := c.do(ctx, http.MethodGet, waitPath, nil)
	if err != nil {
		return err
	}
	if code >= 400 || env.Error != "" {
		return fmt.Errorf("incus operation %s failed (%d): %s", opPath, code, env.Error)
	}
	var meta struct {
		Status string `json:"status"`
		Err    string `json:"err"`
	}
	if json.Unmarshal(env.Metadata, &meta) == nil && meta.Err != "" {
		return fmt.Errorf("incus operation %s error: %s", opPath, meta.Err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*apiEnvelope, int, error) {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	var env apiEnvelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	return &env, resp.StatusCode, nil
}
