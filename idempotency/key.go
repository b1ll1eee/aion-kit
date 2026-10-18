package idempotency

import "fmt"

// MaxKeyLen is the longest key accepted, matching common payment APIs.
const MaxKeyLen = 255

// Key identifies one logical operation chosen by the client, usually a UUID.
// The zero Key is invalid; obtain one with [ParseKey].
type Key struct{ s string }

// ParseKey validates s and returns it as a Key. A key must be 1 to
// [MaxKeyLen] bytes of visible ASCII (0x21-0x7E).
func ParseKey(s string) (Key, error) {
	if len(s) == 0 || len(s) > MaxKeyLen {
		return Key{}, fmt.Errorf("%w: length %d not in 1..%d", ErrInvalidKey, len(s), MaxKeyLen)
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e {
			return Key{}, fmt.Errorf("%w: byte 0x%02x at %d", ErrInvalidKey, c, i)
		}
	}
	return Key{s: s}, nil
}

// String returns the key as sent by the client.
func (k Key) String() string { return k.s }

// IsZero reports whether k is the zero Key.
func (k Key) IsZero() bool { return k.s == "" }
