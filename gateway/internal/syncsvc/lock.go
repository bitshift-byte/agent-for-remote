package syncsvc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrLockConflict = errors.New("workspace lock held by another active device")
	ErrLockInvalid  = errors.New("invalid or expired device lease token")
)

type DeviceLock struct {
	Username      string    `json:"username"`
	Mount         string    `json:"mount,omitempty"`
	DeviceID      string    `json:"device_id"`
	DeviceName    string    `json:"device_name"`
	OS            string    `json:"os"`
	LeaseToken    string    `json:"lease_token,omitempty"`
	Epoch         int64     `json:"epoch"`
	AcquiredAt    time.Time `json:"acquired_at"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type AcquireRequest struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	OS         string `json:"os"`
	Mount      string `json:"mount,omitempty"`
	Force      bool   `json:"force"`
}

type AcquireResult struct {
	Lock        *DeviceLock `json:"lock"`
	TakenOver   bool        `json:"taken_over"`
	PreviousDev string      `json:"previous_device,omitempty"`
}

type LockManager struct {
	ttl   time.Duration
	mu    sync.RWMutex
	locks map[string]*DeviceLock
	epoch map[string]int64
}

func NewLockManager(ttl time.Duration) *LockManager {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &LockManager{
		ttl:   ttl,
		locks: make(map[string]*DeviceLock),
		epoch: make(map[string]int64),
	}
}

func lockKey(username, mount string) string {
	if mount == "" {
		return username
	}
	return username + ":" + mount
}

// Get returns the active root lock for username, or the most recently active mount lock if no root lock exists.
func (lm *LockManager) Get(username string) *DeviceLock {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	now := time.Now().UTC()
	if cur, ok := lm.locks[username]; ok && !now.After(cur.ExpiresAt) {
		cp := *cur
		cp.LeaseToken = ""
		return &cp
	}

	var latest *DeviceLock
	prefix := username + ":"
	for k, cur := range lm.locks {
		if !strings.HasPrefix(k, prefix) || now.After(cur.ExpiresAt) {
			continue
		}
		if latest == nil || cur.LastHeartbeat.After(latest.LastHeartbeat) {
			cp := *cur
			cp.LeaseToken = ""
			latest = &cp
		}
	}
	return latest
}

// GetMount returns the active lock for a specific username + mount (without exposing LeaseToken).
func (lm *LockManager) GetMount(username, mount string) *DeviceLock {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	cur, ok := lm.locks[lockKey(username, mount)]
	if !ok || time.Now().UTC().After(cur.ExpiresAt) {
		return nil
	}
	cp := *cur
	cp.LeaseToken = ""
	return &cp
}

// ListUserLocks returns all active, non-expired locks for username across all devices and mounts.
func (lm *LockManager) ListUserLocks(username string) []*DeviceLock {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	now := time.Now().UTC()
	prefix := username + ":"
	out := make([]*DeviceLock, 0)
	for k, cur := range lm.locks {
		if k != username && !strings.HasPrefix(k, prefix) {
			continue
		}
		if now.After(cur.ExpiresAt) {
			continue
		}
		cp := *cur
		cp.LeaseToken = ""
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviceName != out[j].DeviceName {
			return out[i].DeviceName < out[j].DeviceName
		}
		return out[i].Mount < out[j].Mount
	})
	return out
}

// Acquire grants or renews the active device lock for username (and optional req.Mount).
// If another active device holds the same lock scope and req.Force is false, returns ErrLockConflict.
func (lm *LockManager) Acquire(username string, req AcquireRequest) (*AcquireResult, *DeviceLock, error) {
	normMount, err := NormalizeMountPath(req.Mount)
	if err != nil {
		return nil, nil, err
	}
	key := lockKey(username, normMount)

	lm.mu.Lock()
	defer lm.mu.Unlock()

	now := time.Now().UTC()
	cur, ok := lm.locks[key]
	if ok && now.After(cur.ExpiresAt) {
		delete(lm.locks, key)
		ok = false
	}

	if ok && cur.DeviceID == req.DeviceID {
		cur.LastHeartbeat = now
		cur.ExpiresAt = now.Add(lm.ttl)
		if req.DeviceName != "" {
			cur.DeviceName = req.DeviceName
		}
		cp := *cur
		return &AcquireResult{Lock: &cp, TakenOver: false}, nil, nil
	}

	if ok && !req.Force {
		holder := *cur
		holder.LeaseToken = ""
		return nil, &holder, ErrLockConflict
	}

	prevName := ""
	takenOver := false
	if ok {
		prevName = cur.DeviceName
		if prevName == "" {
			prevName = cur.DeviceID
		}
		takenOver = true
	}

	lm.epoch[key]++
	token := generateLeaseToken()
	next := &DeviceLock{
		Username:      username,
		Mount:         normMount,
		DeviceID:      req.DeviceID,
		DeviceName:    req.DeviceName,
		OS:            req.OS,
		LeaseToken:    token,
		Epoch:         lm.epoch[key],
		AcquiredAt:    now,
		LastHeartbeat: now,
		ExpiresAt:     now.Add(lm.ttl),
	}
	lm.locks[key] = next
	cp := *next
	return &AcquireResult{
		Lock:        &cp,
		TakenOver:   takenOver,
		PreviousDev: prevName,
	}, nil, nil
}

// Heartbeat renews the root lock for username.
func (lm *LockManager) Heartbeat(username, deviceID, leaseToken string) (*DeviceLock, error) {
	return lm.HeartbeatMount(username, "", deviceID, leaseToken)
}

// HeartbeatMount renews the lock for a specific username + mount.
func (lm *LockManager) HeartbeatMount(username, mount, deviceID, leaseToken string) (*DeviceLock, error) {
	normMount, err := NormalizeMountPath(mount)
	if err != nil {
		return nil, err
	}
	key := lockKey(username, normMount)

	lm.mu.Lock()
	defer lm.mu.Unlock()

	now := time.Now().UTC()
	cur, ok := lm.locks[key]
	if !ok || now.After(cur.ExpiresAt) {
		return nil, ErrLockInvalid
	}
	if cur.DeviceID != deviceID || cur.LeaseToken != leaseToken {
		return nil, ErrLockConflict
	}

	cur.LastHeartbeat = now
	cur.ExpiresAt = now.Add(lm.ttl)
	cp := *cur
	return &cp, nil
}

// VerifyWrite validates that the caller holds the active root lock before mutating cloud files.
func (lm *LockManager) VerifyWrite(username, deviceID, leaseToken string) error {
	return lm.VerifyWriteMount(username, "", deviceID, leaseToken)
}

// VerifyWriteMount validates that the caller holds the active lock for username + mount.
func (lm *LockManager) VerifyWriteMount(username, mount, deviceID, leaseToken string) error {
	normMount, err := NormalizeMountPath(mount)
	if err != nil {
		return err
	}
	key := lockKey(username, normMount)

	lm.mu.RLock()
	defer lm.mu.RUnlock()

	now := time.Now().UTC()
	cur, ok := lm.locks[key]
	if !ok || now.After(cur.ExpiresAt) {
		return ErrLockInvalid
	}
	if cur.DeviceID != deviceID || cur.LeaseToken != leaseToken {
		return ErrLockConflict
	}
	return nil
}

// VerifyAnyDeviceLease checks whether deviceID + leaseToken matches any active lock held by this user.
// Used by device-level channels (exec poll, preview tunnel) when a device holds one or more mount locks.
func (lm *LockManager) VerifyAnyDeviceLease(username, deviceID, leaseToken string) error {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	now := time.Now().UTC()
	prefix := username + ":"
	foundActive := false
	for k, cur := range lm.locks {
		if k != username && !strings.HasPrefix(k, prefix) {
			continue
		}
		if now.After(cur.ExpiresAt) {
			continue
		}
		foundActive = true
		if cur.DeviceID == deviceID && cur.LeaseToken == leaseToken {
			return nil
		}
	}
	if !foundActive {
		return ErrLockInvalid
	}
	return ErrLockConflict
}

// Release frees the root lock if held by deviceID + leaseToken.
func (lm *LockManager) Release(username, deviceID, leaseToken string) bool {
	return lm.ReleaseMount(username, "", deviceID, leaseToken)
}

// ReleaseMount frees a specific mount lock if held by deviceID + leaseToken.
func (lm *LockManager) ReleaseMount(username, mount, deviceID, leaseToken string) bool {
	normMount, err := NormalizeMountPath(mount)
	if err != nil {
		return false
	}
	key := lockKey(username, normMount)

	lm.mu.Lock()
	defer lm.mu.Unlock()

	cur, ok := lm.locks[key]
	if !ok {
		return false
	}
	if cur.DeviceID == deviceID && cur.LeaseToken == leaseToken {
		delete(lm.locks, key)
		return true
	}
	return false
}

// ForceReleaseDevice releases all active locks held by deviceID for username (used when revoking a bound device).
func (lm *LockManager) ForceReleaseDevice(username, deviceID string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	prefix := username + ":"
	for k, cur := range lm.locks {
		if (k == username || strings.HasPrefix(k, prefix)) && cur.DeviceID == deviceID {
			delete(lm.locks, k)
		}
	}
}

func generateLeaseToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
