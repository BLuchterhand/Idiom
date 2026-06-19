package intentloop

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sha256Prefixed(value string) string {
	return "sha256:" + sha256Hex(value)
}
