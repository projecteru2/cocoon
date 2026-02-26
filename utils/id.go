package utils

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateID returns a random 16-character hex string (8 bytes of entropy).
func GenerateID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
