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
	"strings"
)

type PairClaimResponse struct {
	DeviceID    string `json:"device_id"`
	DeviceName  string `json:"device_name"`
	OS          string `json:"os"`
	Username    string `json:"username"`
	DeviceToken string `json:"device_token"`
}

// ParseBindInput parses either a full bind string (`super-agent://pair?server=...&code=XXX-XXX`)
// or a plain 6-character pairing code (`XXX-XXX` / `XXXXXX`).
func ParseBindInput(input, fallbackServer string) (serverURL, code string, err error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", "", errors.New("请输入 6 位配对码或粘贴网页复制的绑定口令")
	}

	if strings.HasPrefix(raw, "super-agent://") || strings.Contains(raw, "code=") {
		u, parseErr := url.Parse(raw)
		if parseErr == nil {
			q := u.Query()
			if s := strings.TrimSpace(q.Get("server")); s != "" {
				serverURL = s
			}
			if c := strings.TrimSpace(q.Get("code")); c != "" {
				code = c
			}
		}
	}

	if code == "" {
		code = raw
	}
	if serverURL == "" {
		serverURL = strings.TrimSpace(fallbackServer)
	}
	if serverURL == "" {
		return "", "", errors.New("缺少云端网关地址，请粘贴完整绑定口令或填写网关地址")
	}
	return strings.TrimRight(serverURL, "/"), strings.TrimSpace(code), nil
}

// ClaimPairingCode exchanges a 6-character pairing code for a dedicated DeviceToken (no password needed).
func (c *Client) ClaimPairingCode(ctx context.Context, code, deviceID, deviceName, osName string) (*PairClaimResponse, error) {
	payload, _ := json.Marshal(map[string]string{
		"code":        code,
		"device_id":   deviceID,
		"device_name": deviceName,
		"os":          osName,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/devices/pair/claim", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var errBody struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
			return nil, errors.New(errBody.Error)
		}
		return nil, fmt.Errorf("配对失败 (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out PairClaimResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.DeviceToken == "" {
		return nil, errors.New("服务端未返回有效设备凭证")
	}
	c.token = out.DeviceToken
	c.deviceID = out.DeviceID
	return &out, nil
}
