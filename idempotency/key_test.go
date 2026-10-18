package idempotency_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/b1ll1eee/aion-kit/idempotency"
)

func TestParseKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"uuid", "4f1c2a9e-7d1b-4c55-9a3e-0b6f1d2e3c4a", true},
		{"single char", "x", true},
		{"max length", strings.Repeat("k", idempotency.MaxKeyLen), true},
		{"empty", "", false},
		{"too long", strings.Repeat("k", idempotency.MaxKeyLen+1), false},
		{"space", "a b", false},
		{"newline", "a\nb", false},
		{"non-ascii", "คีย์", false},
		{"del", "a\x7f", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, err := idempotency.ParseKey(tt.in)
			if tt.ok {
				if err != nil || k.String() != tt.in {
					t.Fatalf("ParseKey(%q) = %q, %v", tt.in, k, err)
				}
				return
			}
			if !errors.Is(err, idempotency.ErrInvalidKey) || !k.IsZero() {
				t.Fatalf("ParseKey(%q) = %q, %v; want ErrInvalidKey", tt.in, k, err)
			}
		})
	}
}

func FuzzParseKey(f *testing.F) {
	for _, s := range []string{"", "abc", "a b", "\x00", strings.Repeat("z", 300)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k, err := idempotency.ParseKey(s)
		if err != nil {
			return
		}
		if k.String() != s || len(s) == 0 || len(s) > idempotency.MaxKeyLen {
			t.Fatalf("accepted bad key %q", s)
		}
	})
}

func TestFingerprintOfIsUnambiguous(t *testing.T) {
	a := idempotency.FingerprintOf([]byte("ab"), []byte("c"))
	b := idempotency.FingerprintOf([]byte("a"), []byte("bc"))
	if a == b {
		t.Fatal(`FingerprintOf("ab","c") == FingerprintOf("a","bc")`)
	}
	if a != idempotency.FingerprintOf([]byte("ab"), []byte("c")) {
		t.Fatal("FingerprintOf is not deterministic")
	}
}
