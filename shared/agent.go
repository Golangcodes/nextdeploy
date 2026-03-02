package shared

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func GenerateCommandID() string {
	b := make([]byte, 16) // 128-bit ID
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
