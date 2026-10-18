package idempotency_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/b1ll1eee/aion-kit/idempotency"
)

var okBody = []byte(`{"id":"pay_123","status":"succeeded"}`)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Write(okBody)
})

// discard is a ResponseWriter that allocates nothing, so the benchmarks
// measure the middleware rather than httptest.
type discard struct{ h http.Header }

func (d *discard) Header() http.Header         { return d.h }
func (d *discard) Write(p []byte) (int, error) { return len(p), nil }
func (d *discard) WriteHeader(int)             {}

func BenchmarkNoKey(b *testing.B) {
	h := idempotency.Middleware(idempotency.NewMemoryStore())(okHandler)
	r := httptest.NewRequest(http.MethodPost, "/payments", nil)
	w := &discard{h: http.Header{}}
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkFirstRequest(b *testing.B) {
	h := idempotency.Middleware(idempotency.NewMemoryStore())(okHandler)
	const body = `{"amount":100,"currency":"THB"}`
	w := &discard{h: http.Header{}}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		r := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(body))
		r.Header.Set(idempotency.DefaultHeader, "key-"+strconv.Itoa(i))
		i++
		h.ServeHTTP(w, r)
	}
}

func BenchmarkReplay(b *testing.B) {
	h := idempotency.Middleware(idempotency.NewMemoryStore())(okHandler)
	const body = `{"amount":100,"currency":"THB"}`
	newReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(body))
		r.Header.Set(idempotency.DefaultHeader, "key-1")
		return r
	}
	h.ServeHTTP(&discard{h: http.Header{}}, newReq())
	b.ReportAllocs()
	for b.Loop() {
		h.ServeHTTP(&discard{h: http.Header{}}, newReq())
	}
}
