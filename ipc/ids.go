package ipc

import (
	"crypto/rand"
	"encoding/hex"
)

func newID() string {
	id, err := RandomToken(12)
	if err != nil {
		return ""
	}
	return id
}

func RandomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
