package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func NowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func NewID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	if prefix == "" {
		return hex.EncodeToString(buf)
	}
	return prefix + "_" + hex.EncodeToString(buf)
}
