package google

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

type TokenCipher struct{ aead cipher.AEAD }

func NewTokenCipher(encodedKey string) (*TokenCipher, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("encryption key must decode to 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &TokenCipher{aead: aead}, nil
}

func (c *TokenCipher) EncryptToken(token string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(token), []byte("eggy-google-refresh-token-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *TokenCipher) DecryptToken(encoded string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted token: %w", err)
	}
	if len(data) < c.aead.NonceSize() {
		return "", errors.New("encrypted token is truncated")
	}
	nonce, ciphertext := data[:c.aead.NonceSize()], data[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, ciphertext, []byte("eggy-google-refresh-token-v1"))
	if err != nil {
		return "", errors.New("encrypted token authentication failed")
	}
	return string(plain), nil
}
