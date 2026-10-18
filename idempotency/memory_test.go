package idempotency_test

import (
	"sync"
	"testing"
	"time"

	"github.com/b1ll1eee/aion-kit/idempotency"
	"github.com/b1ll1eee/aion-kit/idempotency/idempotencytest"
)

// fakeClock is shared by every store the suite creates.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(_ *testing.T, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestMemoryStore(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	idempotencytest.RunStoreSuite(t, idempotencytest.Harness{
		NewStore: func(*testing.T) idempotency.Store {
			s := idempotency.NewMemoryStore()
			s.SetClock(clock.Now)
			return s
		},
		Advance: clock.Advance,
	})
}

func TestMemoryStoreSweepsExpired(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	s := idempotency.NewMemoryStore()
	s.SetClock(clock.Now)
	g := idempotency.NewGuard(s, idempotency.WithLockTimeout(time.Second))
	for i := range 10 {
		k, _ := idempotency.ParseKey(string(rune('a' + i)))
		if _, err := g.Begin(t.Context(), k, idempotency.Fingerprint{}); err != nil {
			t.Fatal(err)
		}
	}
	if s.Len() != 10 {
		t.Fatalf("Len = %d, want 10", s.Len())
	}
	clock.Advance(t, 2*time.Second)
	if s.Len() != 0 {
		t.Fatalf("Len after expiry = %d, want 0", s.Len())
	}
}
