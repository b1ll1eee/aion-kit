package idempotency

import (
	"context"
	"sync"
	"time"
)

// sweepEvery is how many acquires pass between full scans for expired
// records. Expired records are also dropped lazily when touched.
const sweepEvery = 1024

// MemoryStore is an in-process [Store]. It suits tests and single-instance
// services; replicas behind a load balancer need a shared store such as
// redisstore.
type MemoryStore struct {
	mu       sync.Mutex
	items    map[Key]memItem
	acquires int
	now      func() time.Time
}

type memItem struct {
	rec     Record
	expires time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[Key]memItem), now: time.Now}
}

// Acquire implements [Store].
func (s *MemoryStore) Acquire(_ context.Context, rec Record, lockTTL time.Duration) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.acquires++; s.acquires%sweepEvery == 0 {
		s.sweep(now)
	}
	if it, ok := s.get(rec.Key, now); ok {
		return it.rec, false, nil
	}
	s.items[rec.Key] = memItem{rec: rec, expires: now.Add(lockTTL)}
	return Record{}, true, nil
}

// Complete implements [Store].
func (s *MemoryStore) Complete(_ context.Context, key Key, lease Lease, result []byte, retention time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	it, ok := s.get(key, now)
	if !ok {
		return ErrLeaseLost
	}
	done, err := it.rec.Complete(lease, result)
	if err != nil {
		return err
	}
	s.items[key] = memItem{rec: done, expires: now.Add(retention)}
	return nil
}

// Release implements [Store].
func (s *MemoryStore) Release(_ context.Context, key Key, lease Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.get(key, s.now())
	if !ok {
		return ErrLeaseLost
	}
	if err := it.rec.CheckLease(lease); err != nil {
		return err
	}
	delete(s.items, key)
	return nil
}

// Len returns the number of live records.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.now())
	return len(s.items)
}

// get returns the live item for key, deleting it if expired. Caller holds mu.
func (s *MemoryStore) get(key Key, now time.Time) (memItem, bool) {
	it, ok := s.items[key]
	if ok && !now.Before(it.expires) {
		delete(s.items, key)
		return memItem{}, false
	}
	return it, ok
}

func (s *MemoryStore) sweep(now time.Time) {
	for k, it := range s.items {
		if !now.Before(it.expires) {
			delete(s.items, k)
		}
	}
}
