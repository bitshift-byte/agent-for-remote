package docker

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

const apiVersion = "v1.43"

var ErrNotFound = errors.New("docker resource not found")

type Client struct {
	httpClient *http.Client
	baseURL    string
}

func NewClient(dockerHost string) (*Client, error) {
	if dockerHost == "" {
		dockerHost = "unix:///var/run/docker.sock"
	}

	if strings.HasPrefix(dockerHost, "unix://") {
		socketPath := strings.TrimPrefix(dockerHost, "unix://")
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		}
		return &Client{
			httpClient: &http.Client{
				Transport: transport,
				Timeout:   30 * time.Second,
			},
			baseURL: "http://docker/" + apiVersion,
		}, nil
	}

	u, err := url.Parse(dockerHost)
	if err != nil {
		return nil, fmt.Errorf("invalid DOCKER_HOST %q: %w", dockerHost, err)
	}
	scheme := u.Scheme
	if scheme == "tcp" {
		scheme = "http"
	}
	baseURL := fmt.Sprintf("%s://%s/%s", scheme, u.Host, apiVersion)
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
	}, nil
}

type ContainerInspect struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
		Paused  bool   `json:"Paused"`
	} `json:"State"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

type CreateContainerRequest struct {
	Image            string            `json:"Image"`
	Env              []string          `json:"Env,omitempty"`
	Labels           map[string]string `json:"Labels,omitempty"`
	HostConfig       HostConfig        `json:"HostConfig"`
	NetworkingConfig *NetworkingConfig `json:"NetworkingConfig,omitempty"`
}

type HostConfig struct {
	Binds         []string      `json:"Binds,omitempty"`
	Memory        int64         `json:"Memory,omitempty"`
	NanoCpus      int64         `json:"NanoCpus,omitempty"`
	ShmSize       int64         `json:"ShmSize,omitempty"`
	RestartPolicy RestartPolicy `json:"RestartPolicy,omitempty"`
	SecurityOpt   []string      `json:"SecurityOpt,omitempty"`
}

type RestartPolicy struct {
	Name string `json:"Name"`
}

type NetworkingConfig struct {
	EndpointsConfig map[string]EndpointSettings `json:"EndpointsConfig"`
}

type EndpointSettings struct{}

type CreateContainerResponse struct {
	ID string `json:"Id"`
}

func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodGet, "/networks/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("inspect network %q failed: status %d", name, resp.StatusCode)
	}

	payload := map[string]any{
		"Name":           name,
		"Driver":         "bridge",
		"CheckDuplicate": true,
	}
	createResp, err := c.do(ctx, http.MethodPost, "/networks/create", payload)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(createResp.Body, 1024))
		return fmt.Errorf("create network %q failed (%d): %s", name, createResp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) InspectContainer(ctx context.Context, nameOrID string) (*ContainerInspect, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(nameOrID)+"/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("inspect container %q failed (%d): %s", nameOrID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var info ContainerInspect
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *Client) CreateContainer(ctx context.Context, name string, req CreateContainerRequest) (string, error) {
	path := "/containers/create?name=" + url.QueryEscape(name)
	resp, err := c.do(ctx, http.MethodPost, path, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("create container %q failed (%d): %s", name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var created CreateContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (c *Client) StartContainer(ctx context.Context, nameOrID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(nameOrID)+"/start", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("start container %q failed (%d): %s", nameOrID, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *Client) StopContainer(ctx context.Context, nameOrID string, timeoutSec int) error {
	path := fmt.Sprintf("/containers/%s/stop?t=%d", url.PathEscape(nameOrID), timeoutSec)
	resp, err := c.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("stop container %q failed (%d): %s", nameOrID, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *Client) UnpauseContainer(ctx context.Context, nameOrID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(nameOrID)+"/unpause", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return fmt.Errorf("unpause container %q failed (%d): %s", nameOrID, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}
