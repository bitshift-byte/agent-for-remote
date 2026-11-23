package syncer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TunnelRequest struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Port    int                 `json:"port"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers,omitempty"`
	BodyB64 string              `json:"body_b64,omitempty"`
}

type TunnelResponse struct {
	ReqID   string              `json:"req_id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	BodyB64 string              `json:"body_b64,omitempty"`
	Error   string              `json:"error,omitempty"`
}

func (c *Client) PollTunnelRequest(ctx context.Context, timeoutSec int) (*TunnelRequest, error) {
	if timeoutSec <= 0 || timeoutSec > 55 {
		timeoutSec = 25
	}
	endpoint := "/api/tunnel/poll?timeout=" + strconv.Itoa(timeoutSec)
	req, err := c.newAuthedRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Device-ID", c.deviceID)
	req.Header.Set("X-Lease-Token", c.leaseToken)
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
		return nil, fmt.Errorf("poll tunnel failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out TunnelRequest
	if resp.Header.Get(HeaderEnc) == EncSchemeAES256GCM {
		if len(c.payloadKey) != 32 {
			return nil, errors.New("received encrypted tunnel request without local AES-256 key")
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
		plain, err := DecryptAESGCM(c.payloadKey, sealed, []byte("tunnel-req"))
		if err != nil {
			return nil, fmt.Errorf("decrypt tunnel request failed: %w", err)
		}
		if err := json.Unmarshal(plain, &out); err != nil {
			return nil, err
		}
	} else {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

func (c *Client) SubmitTunnelResponse(ctx context.Context, res TunnelResponse) error {
	plain, err := json.Marshal(res)
	if err != nil {
		return err
	}
	bodyBytes := plain
	encrypted := false
	if len(c.payloadKey) == 32 {
		sealed, encErr := EncryptAESGCM(c.payloadKey, plain, []byte("tunnel-resp"))
		if encErr != nil {
			return encErr
		}
		bodyBytes, err = json.Marshal(map[string]string{
			"enc":     EncSchemeAES256GCM,
			"payload": base64.StdEncoding.EncodeToString(sealed),
		})
		if err != nil {
			return err
		}
		encrypted = true
	}

	req, err := c.newAuthedRequest(ctx, http.MethodPost, "/api/tunnel/response", bytes.NewReader(bodyBytes))
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
		return fmt.Errorf("submit tunnel response failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// RunTunnelLoop long-polls the gateway for preview requests and forwards them strictly to local loopback dev servers.
func RunTunnelLoop(ctx context.Context, client *Client, onLockLost func()) {
	localHTTP := &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	log.Println("[tunnel] local preview tunnel online — cloud browser can preview local dev ports via gateway")

	for {
		if ctx.Err() != nil {
			return
		}
		treq, err := client.PollTunnelRequest(ctx, 25)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ErrLockLost) {
				if onLockLost != nil {
					onLockLost()
				}
				return
			}
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}
		if treq == nil {
			continue
		}

		go func(tr *TunnelRequest) {
			resp := handleLocalTunnelRequest(ctx, localHTTP, tr)
			if err := client.SubmitTunnelResponse(ctx, resp); err != nil && errors.Is(err, ErrLockLost) {
				if onLockLost != nil {
					onLockLost()
				}
			}
		}(treq)
	}
}

func handleLocalTunnelRequest(ctx context.Context, httpClient *http.Client, tr *TunnelRequest) TunnelResponse {
	res := TunnelResponse{ReqID: tr.ID}
	if tr.Port < 1 || tr.Port > 65535 || tr.Port == 17890 {
		res.Status = http.StatusForbidden
		res.Error = "port not allowed for local preview tunnel"
		return res
	}
	var bodyReader io.Reader
	if tr.BodyB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(tr.BodyB64)
		if err != nil {
			res.Status = http.StatusBadRequest
			res.Error = "invalid request body encoding"
			return res
		}
		bodyReader = bytes.NewReader(raw)
	}
	targetURL := fmt.Sprintf("http://localhost:%d%s", tr.Port, tr.Path)
	req, err := http.NewRequestWithContext(ctx, tr.Method, targetURL, bodyReader)
	if err != nil {
		res.Status = http.StatusBadGateway
		res.Error = err.Error()
		return res
	}
	for k, vals := range tr.Headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		res.Status = http.StatusBadGateway
		res.Error = fmt.Sprintf("local port %d unreachable: %v", tr.Port, err)
		return res
	}
	defer resp.Body.Close()

	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		res.Status = http.StatusBadGateway
		res.Error = err.Error()
		return res
	}
	res.Status = resp.StatusCode
	res.Headers = resp.Header
	if len(rawResp) > 0 {
		res.BodyB64 = base64.StdEncoding.EncodeToString(rawResp)
	}
	return res
}
