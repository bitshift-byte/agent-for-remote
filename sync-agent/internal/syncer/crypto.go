package syncer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	EncSchemeAES256GCM = "aes-256-gcm-v1"
	HeaderEnc          = "X-SA-Enc"
	HeaderAcceptEnc    = "X-SA-Accept-Enc"
)

var (
	ErrDecryptFailed = errors.New("AES-256-GCM payload decryption failed")
	ErrInvalidKey    = errors.New("invalid AES-256 key length")
)

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

func normalizePairCodeLocal(code string) string {
	s := strings.ToUpper(strings.TrimSpace(code))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

func derivePairWrappingKey(pairCode, deviceID string, salt []byte) []byte {
	h := sha256.New()
	h.Write([]byte("super-agent-pair-wrap-v1:"))
	h.Write([]byte(normalizePairCodeLocal(pairCode)))
	h.Write([]byte(":"))
	h.Write([]byte(deviceID))
	h.Write([]byte(":"))
	h.Write(salt)
	return h.Sum(nil)
}

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
