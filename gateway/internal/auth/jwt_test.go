package auth

import (
	"testing"
	"time"
)

func TestSignAndVerify(t *testing.T) {
	mgr := NewManager("super-secret-key-for-test", time.Hour)

	token, err := mgr.Sign("alice")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	claims, err := mgr.Verify(token)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if claims.Sub != "alice" {
		t.Fatalf("expected sub=alice, got %q", claims.Sub)
	}
}

func TestVerifyTamperedToken(t *testing.T) {
	mgr := NewManager("super-secret-key-for-test", time.Hour)
	token, err := mgr.Sign("alice")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if _, err := mgr.Verify(token + "x"); err == nil {
		t.Fatalf("expected tampered token to fail verification")
	}
}

func TestVerifyExpiredToken(t *testing.T) {
	mgr := NewManager("super-secret-key-for-test", -time.Second)
	token, err := mgr.Sign("alice")
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	if _, err := mgr.Verify(token); err != ErrExpiredToken {
		t.Fatalf("expected ErrExpiredToken, got %v", err)
	}
}
