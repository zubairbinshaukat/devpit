package elevate

import (
	"crypto/rand"
	"encoding/hex"
)

// randomHex returns n random bytes, hex-encoded. It backs both pipe-name
// suffixes and request IDs: Devpit does not need RFC 4122 UUIDs, only enough
// entropy that two concurrent pipes or two in-flight requests never collide.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newID returns a request ID. crypto/rand failing is not something Devpit
// expects to happen on a real machine; if it ever does, a fixed fallback
// keeps the client usable instead of panicking.
func newID() string {
	id, err := randomHex(8)
	if err != nil {
		return "id-fallback"
	}
	return id
}
