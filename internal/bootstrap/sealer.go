package bootstrap

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type Sealer struct {
	aead cipher.AEAD
}

func LoadSealer(path string) (*Sealer, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat JIT encryption key: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("JIT encryption key must not be group- or world-accessible")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JIT encryption key: %w", err)
	}
	key, err := decodeKey(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

func (s *Sealer) Seal(leaseID string, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("cannot seal empty JIT configuration")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plaintext, []byte(leaseID)), nil
}

func (s *Sealer) Open(leaseID string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < s.aead.NonceSize() {
		return nil, errors.New("invalid JIT ciphertext")
	}
	nonce := ciphertext[:s.aead.NonceSize()]
	return s.aead.Open(nil, nonce, ciphertext[s.aead.NonceSize():], []byte(leaseID))
}

func decodeKey(value string) ([]byte, error) {
	if key, err := hex.DecodeString(value); err == nil && len(key) == 32 {
		return key, nil
	}
	if key, err := base64.StdEncoding.DecodeString(value); err == nil && len(key) == 32 {
		return key, nil
	}
	return nil, errors.New("JIT encryption key must be 32 bytes encoded as hex or base64")
}
