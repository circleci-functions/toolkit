// Package testrand offers helpers for generating random data in tests
package testrand

import (
	"crypto/rand"
	"encoding/hex"
)

// Hex produces an insecure random number hex encoded to a string of b characters.
// If b is odd the resultant string won't be valid hex, but will still only contain
// characters that can appear in a hex encoding.
func Hex(n int) string {
	b := make([]byte, n/2+1)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)[:n]
}
