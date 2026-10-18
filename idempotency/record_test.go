package idempotency_test

import (
	"errors"
	"testing"

	"github.com/b1ll1eee/aion-kit/idempotency"
)

func TestRecordTransitions(t *testing.T) {
	k, _ := idempotency.ParseKey("k")
	lease := idempotency.NewLease()
	inFlight := idempotency.NewRecord(k, idempotency.Fingerprint{}, lease)

	done, err := inFlight.Complete(lease, []byte("r"))
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != idempotency.StatusCompleted || done.Lease != "" || string(done.Result) != "r" {
		t.Fatalf("completed record = %+v", done)
	}
	if inFlight.Status != idempotency.StatusInFlight {
		t.Fatal("Complete mutated its receiver")
	}

	if _, err := inFlight.Complete(idempotency.NewLease(), nil); !errors.Is(err, idempotency.ErrLeaseLost) {
		t.Fatalf("Complete with other lease: %v", err)
	}
	if _, err := done.Complete(lease, nil); !errors.Is(err, idempotency.ErrLeaseLost) {
		t.Fatalf("Complete on completed record: %v", err)
	}
	if err := done.CheckLease(lease); !errors.Is(err, idempotency.ErrLeaseLost) {
		t.Fatalf("CheckLease on completed record: %v", err)
	}
}

func TestNewLeaseIsUnique(t *testing.T) {
	seen := map[idempotency.Lease]bool{}
	for range 1000 {
		l := idempotency.NewLease()
		if seen[l] {
			t.Fatalf("duplicate lease %s", l)
		}
		seen[l] = true
	}
}
