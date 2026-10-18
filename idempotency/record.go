package idempotency

import (
	"crypto/rand"
	"encoding/hex"
)

// Status is the lifecycle stage of a [Record].
type Status uint8

const (
	// StatusInFlight means a request holds the lease and is still running.
	StatusInFlight Status = iota + 1
	// StatusCompleted means the result is stored and will be replayed.
	StatusCompleted
)

func (s Status) String() string {
	switch s {
	case StatusInFlight:
		return "in-flight"
	case StatusCompleted:
		return "completed"
	}
	return "unknown"
}

// Lease is a fencing token handed to the request that acquired a key. Only
// the lease holder may complete or release the record, so a request that
// outlived its lock cannot overwrite the work of the request that took over.
type Lease string

// NewLease returns a random 128-bit lease.
func NewLease() Lease {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("idempotency: crypto/rand: " + err.Error())
	}
	return Lease(hex.EncodeToString(b[:]))
}

// Record is the stored state of one key. It is a value: transitions return a
// new Record and never mutate the receiver, so stores that work by
// read-modify-write inside a transaction can reuse the same rules.
//
// Valid transitions:
//
//	(none)    --Acquire-->  in-flight
//	in-flight --Complete--> completed   (lease must match)
//	in-flight --Release-->  (none)      (lease must match)
type Record struct {
	Key         Key
	Fingerprint Fingerprint
	Status      Status
	Lease       Lease  // set while in-flight
	Result      []byte // set once completed; opaque to the core
}

// NewRecord returns an in-flight record held by lease.
func NewRecord(key Key, fp Fingerprint, lease Lease) Record {
	return Record{Key: key, Fingerprint: fp, Status: StatusInFlight, Lease: lease}
}

// Complete returns the completed form of r. It fails with [ErrLeaseLost]
// unless r is in-flight and held by lease.
func (r Record) Complete(lease Lease, result []byte) (Record, error) {
	if err := r.CheckLease(lease); err != nil {
		return r, err
	}
	r.Status = StatusCompleted
	r.Lease = ""
	r.Result = result
	return r, nil
}

// CheckLease reports [ErrLeaseLost] unless r is in-flight and held by lease.
// Stores call it before releasing a record.
func (r Record) CheckLease(lease Lease) error {
	if r.Status != StatusInFlight || r.Lease != lease {
		return ErrLeaseLost
	}
	return nil
}
