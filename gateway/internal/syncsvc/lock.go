package syncsvc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	ErrLockConflict = errors.New("workspace lock held by another active device")
	ErrLockInvalid  = errors.New("invalid or expired device lease token")
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

type AcquireRequest struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	OS         string `json:"os"`
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

// Get returns the active lock for username (without exposing the secret lease token), or nil if unlocked/expired.
func (lm *LockManager) Get(username string) *DeviceLock {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	cur, ok := lm.locks[username]
	if !ok || time.Now().UTC().After(cur.ExpiresAt) {
		return nil
	}
	cp := *cur
	cp.LeaseToken = ""
	return &cp
}

// Acquire grants or renews the active device lock for username.
// If another active device holds the lock and req.Force is false, returns ErrLockConflict along with the holder's info.
func (lm *LockManager) Acquire(username string, req AcquireRequest) (*AcquireResult, *DeviceLock, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	now := time.Now().UTC()
	cur, ok := lm.locks[username]
	if ok && now.After(cur.ExpiresAt) {
		delete(lm.locks, username)
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

	lm.epoch[username]++
	token := generateLeaseToken()
	next := &DeviceLock{
		Username:      username,
		DeviceID:      req.DeviceID,
		DeviceName:    req.DeviceName,
		OS:            req.OS,
		LeaseToken:    token,
		Epoch:         lm.epoch[username],
		AcquiredAt:    now,
		LastHeartbeat: now,
		ExpiresAt:     now.Add(lm.ttl),
	}
	lm.locks[username] = next
	cp := *next
	return &AcquireResult{
		Lock:        &cp,
		TakenOver:   takenOver,
		PreviousDev: prevName,
	}, nil, nil
}

// Heartbeat renews the active device lock. Fails with ErrLockConflict if another device took over.
func (lm *LockManager) Heartbeat(username, deviceID, leaseToken string) (*DeviceLock, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	now := time.Now().UTC()
	cur, ok := lm.locks[username]
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

// VerifyWrite validates that the caller holds the active device lock before mutating cloud files.
func (lm *LockManager) VerifyWrite(username, deviceID, leaseToken string) error {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	now := time.Now().UTC()
	cur, ok := lm.locks[username]
	if !ok || now.After(cur.ExpiresAt) {
		return ErrLockInvalid
	}
	if cur.DeviceID != deviceID || cur.LeaseToken != leaseToken {
		return ErrLockConflict
	}
	return nil
}

// Release frees the lock if held by the specified device and lease token.
func (lm *LockManager) Release(username, deviceID, leaseToken string) bool {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	cur, ok := lm.locks[username]
	if !ok {
		return false
	}
	if cur.DeviceID == deviceID && cur.LeaseToken == leaseToken {
		delete(lm.locks, username)
		return true
	}
	return false
}

func generateLeaseToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
