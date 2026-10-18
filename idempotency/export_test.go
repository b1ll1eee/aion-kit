package idempotency

import "time"

// SetClock lets tests drive MemoryStore expiry without sleeping.
func (s *MemoryStore) SetClock(now func() time.Time) { s.now = now }

var DecodeResponse = decodeResponse
