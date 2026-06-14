package ipc

import (
	"crypto/rand"
	"encoding/hex"
)

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
