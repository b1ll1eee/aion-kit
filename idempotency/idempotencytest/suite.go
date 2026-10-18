// Package idempotencytest holds the contract tests every
// [idempotency.Store] must pass. Run them from the store's own tests:
//
//	func TestStore(t *testing.T) {
//		idempotencytest.RunStoreSuite(t, idempotencytest.Harness{
//			NewStore: func(t *testing.T) idempotency.Store { return newStore(t) },
//		})
//	}
package idempotencytest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ll1eee/aion-kit/idempotency"
)

// Harness adapts a store under test to the suite.
type Harness struct {
	// NewStore returns a store for one subtest. Stores may be shared
	// between subtests; every subtest uses its own random keys.
	NewStore func(t *testing.T) idempotency.Store

	// Advance moves the store's clock forward by d so TTLs can expire.
	// Nil means time.Sleep(d).
	Advance func(t *testing.T, d time.Duration)
}

const (
	lockTTL   = 200 * time.Millisecond
	retention = 400 * time.Millisecond
)

// RunStoreSuite runs every contract test against h.
func RunStoreSuite(t *testing.T, h Harness) {
	if h.Advance == nil {
		h.Advance = func(_ *testing.T, d time.Duration) { time.Sleep(d) }
	}
	for _, tc := range []struct {
		name string
		fn   func(*testing.T, Harness)
	}{
		{"AcquireNewKey", testAcquireNew},
		{"AcquireHeldKeyReturnsExisting", testAcquireHeld},
		{"CompleteThenReplay", testCompleteThenReplay},
		{"CompleteWrongLease", testCompleteWrongLease},
		{"CompleteTwice", testCompleteTwice},
		{"CompleteMissingKey", testCompleteMissing},
		{"ReleaseFreesKey", testReleaseFrees},
		{"ReleaseWrongLease", testReleaseWrongLease},
		{"ReleaseAfterComplete", testReleaseAfterComplete},
		{"LockExpires", testLockExpires},
		{"RetentionExpires", testRetentionExpires},
		{"ResultIsBinarySafe", testBinaryResult},
		{"ConcurrentAcquireHasOneWinner", testConcurrentAcquire},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, h) })
	}
}

func newKey(t *testing.T) idempotency.Key {
	t.Helper()
	var b [8]byte
	rand.Read(b[:])
	k, err := idempotency.ParseKey("aiontest-" + hex.EncodeToString(b[:]))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func fp(s string) idempotency.Fingerprint { return idempotency.FingerprintOf([]byte(s)) }

func acquire(t *testing.T, s idempotency.Store, k idempotency.Key) (idempotency.Lease, idempotency.Record, bool) {
	t.Helper()
	lease := idempotency.NewLease()
	got, ok, err := s.Acquire(context.Background(), idempotency.NewRecord(k, fp("body"), lease), lockTTL)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return lease, got, ok
}

func mustAcquire(t *testing.T, s idempotency.Store, k idempotency.Key) idempotency.Lease {
	t.Helper()
	lease, _, ok := acquire(t, s, k)
	if !ok {
		t.Fatal("Acquire on a free key: acquired = false")
	}
	return lease
}

func complete(s idempotency.Store, k idempotency.Key, l idempotency.Lease, res []byte) error {
	return s.Complete(context.Background(), k, l, res, retention)
}

func wantLeaseLost(t *testing.T, op string, err error) {
	t.Helper()
	if !errors.Is(err, idempotency.ErrLeaseLost) {
		t.Fatalf("%s: err = %v, want ErrLeaseLost", op, err)
	}
}

func testAcquireNew(t *testing.T, h Harness) {
	mustAcquire(t, h.NewStore(t), newKey(t))
}

func testAcquireHeld(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	lease := mustAcquire(t, s, k)
	_, got, ok := acquire(t, s, k)
	if ok {
		t.Fatal("second Acquire: acquired = true")
	}
	if got.Status != idempotency.StatusInFlight || got.Lease != lease || got.Fingerprint != fp("body") {
		t.Fatalf("existing = %+v, want in-flight with first lease and fingerprint", got)
	}
}

func testCompleteThenReplay(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	lease := mustAcquire(t, s, k)
	if err := complete(s, k, lease, []byte("result")); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	_, got, ok := acquire(t, s, k)
	if ok {
		t.Fatal("Acquire after Complete: acquired = true")
	}
	if got.Status != idempotency.StatusCompleted || string(got.Result) != "result" {
		t.Fatalf("existing = %+v, want completed with result", got)
	}
	if got.Fingerprint != fp("body") {
		t.Fatal("fingerprint lost on Complete")
	}
}

func testCompleteWrongLease(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	mustAcquire(t, s, k)
	wantLeaseLost(t, "Complete", complete(s, k, idempotency.NewLease(), nil))
}

func testCompleteTwice(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	lease := mustAcquire(t, s, k)
	if err := complete(s, k, lease, []byte("first")); err != nil {
		t.Fatal(err)
	}
	wantLeaseLost(t, "second Complete", complete(s, k, lease, []byte("second")))
	if _, got, _ := acquire(t, s, k); string(got.Result) != "first" {
		t.Fatalf("result = %q, want first", got.Result)
	}
}

func testCompleteMissing(t *testing.T, h Harness) {
	wantLeaseLost(t, "Complete", complete(h.NewStore(t), newKey(t), idempotency.NewLease(), nil))
}

func testReleaseFrees(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	lease := mustAcquire(t, s, k)
	if err := s.Release(context.Background(), k, lease); err != nil {
		t.Fatalf("Release: %v", err)
	}
	mustAcquire(t, s, k)
}

func testReleaseWrongLease(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	mustAcquire(t, s, k)
	wantLeaseLost(t, "Release", s.Release(context.Background(), k, idempotency.NewLease()))
	if _, _, ok := acquire(t, s, k); ok {
		t.Fatal("key freed by a Release with the wrong lease")
	}
}

func testReleaseAfterComplete(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	lease := mustAcquire(t, s, k)
	if err := complete(s, k, lease, []byte("done")); err != nil {
		t.Fatal(err)
	}
	wantLeaseLost(t, "Release", s.Release(context.Background(), k, lease))
	if _, got, _ := acquire(t, s, k); got.Status != idempotency.StatusCompleted {
		t.Fatal("completed record removed by Release")
	}
}

func testLockExpires(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	stale := mustAcquire(t, s, k)
	h.Advance(t, lockTTL+50*time.Millisecond)
	fresh := mustAcquire(t, s, k)
	// The request that outlived its lock must not clobber the new holder.
	wantLeaseLost(t, "Complete with stale lease", complete(s, k, stale, []byte("stale")))
	if err := complete(s, k, fresh, []byte("fresh")); err != nil {
		t.Fatalf("Complete with fresh lease: %v", err)
	}
}

func testRetentionExpires(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	lease := mustAcquire(t, s, k)
	if err := complete(s, k, lease, []byte("done")); err != nil {
		t.Fatal(err)
	}
	// Retention replaces the lock TTL rather than adding to it.
	h.Advance(t, lockTTL+50*time.Millisecond)
	if _, _, ok := acquire(t, s, k); ok {
		t.Fatal("completed record expired after the lock TTL, want retention")
	}
	h.Advance(t, retention)
	mustAcquire(t, s, k)
}

func testBinaryResult(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	want := []byte{0, 1, 2, 0xff, 0, '\n', 0x80}
	if err := complete(s, k, mustAcquire(t, s, k), want); err != nil {
		t.Fatal(err)
	}
	if _, got, _ := acquire(t, s, k); !bytes.Equal(got.Result, want) {
		t.Fatalf("result = %x, want %x", got.Result, want)
	}
}

func testConcurrentAcquire(t *testing.T, h Harness) {
	s, k := h.NewStore(t), newKey(t)
	const n = 32
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := idempotency.NewRecord(k, fp("body"), idempotency.NewLease())
			_, ok, err := s.Acquire(context.Background(), rec, lockTTL)
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := wins.Load(); got != 1 {
		t.Fatalf("%d of %d concurrent Acquires won, want exactly 1", got, n)
	}
}
