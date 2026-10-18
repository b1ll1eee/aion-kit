package idempotency

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidKey means the client sent a malformed key.
	ErrInvalidKey = errors.New("idempotency: invalid key")
	// ErrMissingKey means the key is required but the client sent none.
	ErrMissingKey = errors.New("idempotency: missing key")
	// ErrInFlight means another request with the same key is still running.
	ErrInFlight = errors.New("idempotency: request with this key is in flight")
	// ErrMismatch means the key was already used for a different payload.
	ErrMismatch = errors.New("idempotency: key reused with a different payload")
	// ErrLeaseLost means the caller no longer holds the record, usually
	// because its lock timed out and another request took the key.
	ErrLeaseLost = errors.New("idempotency: lease lost")
)

// Store is the persistence port. Every method must be atomic with respect
// to the key: two concurrent Acquire calls for one key must never both
// succeed. Implementations should pass idempotencytest.RunStoreSuite.
type Store interface {
	// Acquire stores rec, which is in-flight, if no record exists for
	// rec.Key, and expires it after lockTTL. If a record exists it is
	// returned unchanged with acquired == false.
	Acquire(ctx context.Context, rec Record, lockTTL time.Duration) (existing Record, acquired bool, err error)

	// Complete marks the record completed with result and keeps it for
	// retention. It returns ErrLeaseLost if the record is missing, already
	// completed, or held by another lease.
	Complete(ctx context.Context, key Key, lease Lease, result []byte, retention time.Duration) error

	// Release deletes an in-flight record so the key can be retried. It
	// returns ErrLeaseLost under the same conditions as Complete.
	Release(ctx context.Context, key Key, lease Lease) error
}
