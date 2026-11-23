package syncsvc

import (
	"bytes"
	"testing"
)

func TestAES256GCMRoundTripAndTamperDetection(t *testing.T) {
	key, err := GenerateRandomBytes(32)
	if err != nil {
		t.Fatalf("GenerateRandomBytes failed: %v", err)
	}
	plain := []byte("super-secret source code & remote command output")
	aad := []byte("src/main.go")

	sealed, err := EncryptAESGCM(key, plain, aad)
	if err != nil {
		t.Fatalf("EncryptAESGCM failed: %v", err)
	}
	if bytes.Equal(sealed, plain) {
		t.Fatalf("ciphertext must not equal plaintext")
	}

	got, err := DecryptAESGCM(key, sealed, aad)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("DecryptAESGCM failed: err=%v got=%q", err, string(got))
	}

	// Tampered byte must fail GCM authentication
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := DecryptAESGCM(key, tampered, aad); err != ErrDecryptFailed {
		t.Fatalf("expected ErrDecryptFailed on tampered ciphertext, got %v", err)
	}

	// Wrong AAD must also fail
	if _, err := DecryptAESGCM(key, sealed, []byte("other.go")); err != ErrDecryptFailed {
		t.Fatalf("expected ErrDecryptFailed on wrong AAD, got %v", err)
	}
}

func TestPairCodeKeyWrapAndUnwrap(t *testing.T) {
	sessionKey, _ := GenerateRandomBytes(32)
	wrapped, salt, err := WrapPayloadKey("K7M-4Q9", "dev-mac-1", sessionKey)
	if err != nil {
		t.Fatalf("WrapPayloadKey failed: %v", err)
	}

	unwrapped, err := UnwrapPayloadKey("k7m4q9", "dev-mac-1", wrapped, salt)
	if err != nil || !bytes.Equal(unwrapped, sessionKey) {
		t.Fatalf("UnwrapPayloadKey failed: err=%v", err)
	}

	// Wrong pairing code cannot unwrap the session key
	if _, err := UnwrapPayloadKey("WRONG1", "dev-mac-1", wrapped, salt); err == nil {
		t.Fatalf("expected unwrap with wrong pairing code to fail")
	}
}
