package idempotency

import (
	"context"
	"fmt"
)

// Guard runs the idempotency protocol on top of a [Store]. It knows nothing
// about HTTP, so the same Guard can protect gRPC handlers, queue consumers or
// any other unit of work:
//
//	att, err := g.Begin(ctx, key, fp)
//	if err != nil { ... }                  // ErrInFlight, ErrMismatch, store error
//	if res, ok := att.Replay(); ok { ... } // already done: return res
//	res, err := doWork()
//	if err != nil { g.Abort(ctx, att); ... }
//	g.Complete(ctx, att, res)
type Guard struct {
	store Store
	cfg   config
}

// NewGuard returns a Guard backed by store. Only [WithRetention] and
// [WithLockTimeout] affect a Guard; the other options are for [Middleware].
func NewGuard(store Store, opts ...Option) *Guard {
	return &Guard{store: store, cfg: newConfig(opts)}
}

// Attempt is the outcome of [Guard.Begin]: either a lease to run the work or
// a stored result to replay.
type Attempt struct {
	key    Key
	lease  Lease
	replay []byte
	done   bool
}

// Replay returns the stored result when the key was already completed.
func (a Attempt) Replay() ([]byte, bool) { return a.replay, a.done }

// Key returns the key this attempt is for.
func (a Attempt) Key() Key { return a.key }

// Begin claims key for the caller. It returns [ErrMismatch] if the key was
// used with a different fingerprint and [ErrInFlight] if another caller holds
// it. Otherwise the caller either owns the key or gets a result to replay.
func (g *Guard) Begin(ctx context.Context, key Key, fp Fingerprint) (Attempt, error) {
	lease := NewLease()
	existing, acquired, err := g.store.Acquire(ctx, NewRecord(key, fp, lease), g.cfg.lockTimeout)
	if err != nil {
		return Attempt{}, fmt.Errorf("idempotency: acquire: %w", err)
	}
	if acquired {
		return Attempt{key: key, lease: lease}, nil
	}
	if existing.Fingerprint != fp {
		return Attempt{}, ErrMismatch
	}
	if existing.Status == StatusCompleted {
		return Attempt{key: key, replay: existing.Result, done: true}, nil
	}
	return Attempt{}, ErrInFlight
}

// Complete stores result for att so later attempts replay it.
func (g *Guard) Complete(ctx context.Context, att Attempt, result []byte) error {
	if att.done {
		return nil
	}
	if err := g.store.Complete(ctx, att.key, att.lease, result, g.cfg.retention); err != nil {
		return fmt.Errorf("idempotency: complete %q: %w", att.key, err)
	}
	return nil
}

// Abort gives the key back so the client can retry, for example after a
// transient failure.
func (g *Guard) Abort(ctx context.Context, att Attempt) error {
	if att.done {
		return nil
	}
	if err := g.store.Release(ctx, att.key, att.lease); err != nil {
		return fmt.Errorf("idempotency: release %q: %w", att.key, err)
	}
	return nil
}
