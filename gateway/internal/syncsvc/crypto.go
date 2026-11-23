package syncsvc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	EncSchemeAES256GCM = "aes-256-gcm-v1"
	HeaderEnc          = "X-SA-Enc"
	HeaderAcceptEnc    = "X-SA-Accept-Enc"
)

var (
	ErrDecryptFailed = errors.New("payload decryption or authentication failed")
	ErrInvalidKey    = errors.New("invalid AES-256 key length")
)

// GenerateRandomBytes returns n cryptographically secure random bytes.
func GenerateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}

// EncryptAESGCM encrypts plaintext with AES-256-GCM using a fresh 12-byte random nonce.
// Output format: [12-byte nonce][ciphertext + 16-byte tag].
func EncryptAESGCM(key, plaintext, aad []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

// DecryptAESGCM decrypts [12-byte nonce][ciphertext + tag] using AES-256-GCM and verifies the tag.
func DecryptAESGCM(key, sealed, aad []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns+gcm.Overhead() {
		return nil, ErrDecryptFailed
	}
	nonce, ciphertext := sealed[:ns], sealed[ns:]
	plain, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plain, nil
}

// derivePairWrappingKey derives a 32-byte key from the normalized 6-char pairing code, deviceID, and random salt.
func derivePairWrappingKey(pairCode, deviceID string, salt []byte) []byte {
	h := sha256.New()
	h.Write([]byte("super-agent-pair-wrap-v1:"))
	h.Write([]byte(normalizePairCode(pairCode)))
	h.Write([]byte(":"))
	h.Write([]byte(deviceID))
	h.Write([]byte(":"))
	h.Write(salt)
	return h.Sum(nil)
}

// WrapPayloadKey encrypts a 32-byte session payload key using the 6-char pairing code + random salt.
func WrapPayloadKey(pairCode, deviceID string, payloadKey []byte) (wrappedB64, saltB64 string, err error) {
	salt, err := GenerateRandomBytes(32)
	if err != nil {
		return "", "", err
	}
	wrapKey := derivePairWrappingKey(pairCode, deviceID, salt)
	sealed, err := EncryptAESGCM(wrapKey, payloadKey, []byte("pair-key-wrap:"+deviceID))
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), base64.StdEncoding.EncodeToString(salt), nil
}

// UnwrapPayloadKey decrypts the 32-byte session payload key using the 6-char pairing code + salt.
func UnwrapPayloadKey(pairCode, deviceID, wrappedB64, saltB64 string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(wrappedB64)
	if err != nil {
		return nil, fmt.Errorf("invalid wrapped key encoding: %w", err)
	}
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		return nil, fmt.Errorf("invalid salt encoding: %w", err)
	}
	wrapKey := derivePairWrappingKey(pairCode, deviceID, salt)
	key, err := DecryptAESGCM(wrapKey, sealed, []byte("pair-key-wrap:"+deviceID))
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}
	return key, nil
}

func encodeB64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func decodeB64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
