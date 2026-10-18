package idempotency

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

// Fingerprint is a digest of the request payload. Two requests with the same
// key must have the same fingerprint, or the second one is rejected with
// [ErrMismatch].
type Fingerprint [sha256.Size]byte

// FingerprintOf hashes parts in order. Each part is length-prefixed, so
// ("ab", "c") and ("a", "bc") give different fingerprints.
func FingerprintOf(parts ...[]byte) Fingerprint {
	h := sha256.New()
	var n [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write(p)
	}
	var fp Fingerprint
	h.Sum(fp[:0])
	return fp
}

// String returns the fingerprint as lowercase hex.
func (f Fingerprint) String() string { return hex.EncodeToString(f[:]) }
