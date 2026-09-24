package codegraph

import (
	"crypto/sha256"
	"encoding/hex"
)

func fingerprint(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
