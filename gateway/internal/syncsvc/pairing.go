package syncsvc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	pairingAlphabet   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	pairingTTL        = 5 * time.Minute
	deviceTokenPrefix = "dev_"
)

var (
	ErrInvalidPairCode = errors.New("配对码无效或已过期，请在网页重新生成")
	ErrDeviceNotFound  = errors.New("未找到该设备")
)

type PairingCode struct {
	Code      string    `json:"code"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BoundDevice struct {
	DeviceID    string    `json:"device_id"`
	DeviceName  string    `json:"device_name"`
	OS          string    `json:"os"`
	Username    string    `json:"username"`
	DeviceToken string    `json:"device_token,omitempty"`
	BoundAt     time.Time `json:"bound_at"`
	LastSeen    time.Time `json:"last_seen"`
}

type DeviceRegistry struct {
	mu           sync.RWMutex
	storePath    string
	codes        map[string]*PairingCode // normalized 6-char code (no dash) -> PairingCode
	devices      map[string]*BoundDevice // device_id -> BoundDevice
	tokenToDevID map[string]string       // device_token -> device_id
}

func NewDeviceRegistry(workspaceRoot string) *DeviceRegistry {
	_ = os.MkdirAll(workspaceRoot, 0o755)
	reg := &DeviceRegistry{
		storePath:    filepath.Join(workspaceRoot, ".super-agent-devices.json"),
		codes:        make(map[string]*PairingCode),
		devices:      make(map[string]*BoundDevice),
		tokenToDevID: make(map[string]string),
	}
	reg.load()
	return reg
}

// CreatePairingCode generates a single-use 6-character code formatted as XXX-XXX (valid for 5 minutes).
func (r *DeviceRegistry) CreatePairingCode(username string) (*PairingCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	r.pruneExpiredLocked(now)

	raw, err := randomPairCode(6)
	if err != nil {
		return nil, err
	}
	formatted := raw[:3] + "-" + raw[3:]
	pc := &PairingCode{
		Code:      formatted,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(pairingTTL),
	}
	r.codes[raw] = pc
	cp := *pc
	return &cp, nil
}

// ClaimPairingCode validates and consumes a pairing code, binding the device and issuing a dedicated DeviceToken.
func (r *DeviceRegistry) ClaimPairingCode(code, deviceID, deviceName, osName string) (*BoundDevice, error) {
	norm := normalizePairCode(code)
	if len(norm) != 6 {
		return nil, ErrInvalidPairCode
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, errors.New("device_id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	r.pruneExpiredLocked(now)

	pc, ok := r.codes[norm]
	if !ok || now.After(pc.ExpiresAt) {
		delete(r.codes, norm)
		return nil, ErrInvalidPairCode
	}
	// Single-use: consume immediately
	delete(r.codes, norm)

	// Revoke previous token if this deviceID was previously bound
	if old, exists := r.devices[deviceID]; exists {
		delete(r.tokenToDevID, old.DeviceToken)
	}

	token := deviceTokenPrefix + randomHex(32)
	if strings.TrimSpace(deviceName) == "" {
		deviceName = deviceID
	}
	dev := &BoundDevice{
		DeviceID:    deviceID,
		DeviceName:  strings.TrimSpace(deviceName),
		OS:          strings.TrimSpace(osName),
		Username:    pc.Username,
		DeviceToken: token,
		BoundAt:     now,
		LastSeen:    now,
	}
	r.devices[deviceID] = dev
	r.tokenToDevID[token] = deviceID
	r.saveLocked()

	cp := *dev
	return &cp, nil
}

// VerifyDeviceToken validates a dev_... token and returns its associated username and deviceID.
func (r *DeviceRegistry) VerifyDeviceToken(token string) (string, string, bool) {
	if !strings.HasPrefix(token, deviceTokenPrefix) {
		return "", "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	devID, ok := r.tokenToDevID[token]
	if !ok {
		return "", "", false
	}
	dev, ok := r.devices[devID]
	if !ok {
		return "", "", false
	}
	dev.LastSeen = time.Now().UTC()
	return dev.Username, dev.DeviceID, true
}

// ListByUser returns all bound devices for username with secret tokens redacted.
func (r *DeviceRegistry) ListByUser(username string) []BoundDevice {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]BoundDevice, 0)
	for _, d := range r.devices {
		if d.Username == username {
			cp := *d
			cp.DeviceToken = ""
			out = append(out, cp)
		}
	}
	return out
}

// Revoke removes a bound device owned by username and invalidates its token immediately.
func (r *DeviceRegistry) Revoke(username, deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dev, ok := r.devices[deviceID]
	if !ok || dev.Username != username {
		return ErrDeviceNotFound
	}
	delete(r.tokenToDevID, dev.DeviceToken)
	delete(r.devices, deviceID)
	r.saveLocked()
	return nil
}

func (r *DeviceRegistry) pruneExpiredLocked(now time.Time) {
	for k, v := range r.codes {
		if now.After(v.ExpiresAt) {
			delete(r.codes, k)
		}
	}
}

func (r *DeviceRegistry) load() {
	raw, err := os.ReadFile(r.storePath)
	if err != nil {
		return
	}
	var list []*BoundDevice
	if json.Unmarshal(raw, &list) != nil {
		return
	}
	for _, d := range list {
		if d != nil && d.DeviceID != "" && d.DeviceToken != "" {
			r.devices[d.DeviceID] = d
			r.tokenToDevID[d.DeviceToken] = d.DeviceID
		}
	}
}

func (r *DeviceRegistry) saveLocked() {
	list := make([]*BoundDevice, 0, len(r.devices))
	for _, d := range r.devices {
		list = append(list, d)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(r.storePath, raw, 0o600)
}

func normalizePairCode(code string) string {
	s := strings.ToUpper(strings.TrimSpace(code))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func randomPairCode(length int) (string, error) {
	max := big.NewInt(int64(len(pairingAlphabet)))
	var b strings.Builder
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(pairingAlphabet[n.Int64()])
	}
	return b.String(), nil
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func FormatBindString(serverURL, code string) string {
	return fmt.Sprintf("super-agent://pair?server=%s&code=%s", strings.TrimRight(serverURL, "/"), code)
}
