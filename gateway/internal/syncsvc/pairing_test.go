package syncsvc

import (
	"testing"
)

func TestPairingCodeClaimAndRevoke(t *testing.T) {
	reg := NewDeviceRegistry(t.TempDir())

	pc, err := reg.CreatePairingCode("admin")
	if err != nil {
		t.Fatalf("CreatePairingCode failed: %v", err)
	}
	if len(pc.Code) != 7 || pc.Code[3] != '-' {
		t.Fatalf("unexpected code format: %q", pc.Code)
	}

	// Claim works with lowercase / missing dash
	bound, err := reg.ClaimPairingCode(pc.Code[:3]+pc.Code[4:], "dev-mac-1", "My-MacBook", "darwin")
	if err != nil {
		t.Fatalf("ClaimPairingCode failed: %v", err)
	}
	if bound.Username != "admin" || bound.DeviceToken == "" {
		t.Fatalf("unexpected bound device: %+v", bound)
	}

	// Single-use: second claim with same code must fail
	if _, err := reg.ClaimPairingCode(pc.Code, "dev-mac-2", "Other", "darwin"); err != ErrInvalidPairCode {
		t.Fatalf("expected ErrInvalidPairCode on reuse, got %v", err)
	}

	// Device token verifies cleanly
	user, devID, ok := reg.VerifyDeviceToken(bound.DeviceToken)
	if !ok || user != "admin" || devID != "dev-mac-1" {
		t.Fatalf("VerifyDeviceToken failed: ok=%v user=%q devID=%q", ok, user, devID)
	}

	// Reload from disk preserves bound devices
	reg2 := NewDeviceRegistry(filepathDir(reg.storePath))
	if u, _, ok := reg2.VerifyDeviceToken(bound.DeviceToken); !ok || u != "admin" {
		t.Fatalf("expected persisted device token to verify after reload")
	}

	// Revoke invalidates token immediately
	if err := reg.Revoke("admin", "dev-mac-1"); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if _, _, ok := reg.VerifyDeviceToken(bound.DeviceToken); ok {
		t.Fatalf("expected revoked token to fail verification")
	}
}

func filepathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
