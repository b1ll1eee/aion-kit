package idempotency_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/b1ll1eee/aion-kit/idempotency"
)

// payment is a handler that counts how often it really runs.
type payment struct {
	calls  atomic.Int32
	status int
	gate   chan struct{} // if set, blocks until closed
}

func (p *payment) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := p.calls.Add(1)
	if p.gate != nil {
		<-p.gate
	}
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Charge", string(rune('0'+n)))
	code := p.status
	if code == 0 {
		code = http.StatusCreated
	}
	w.WriteHeader(code)
	io.WriteString(w, `{"charged":`+string(body)+`}`)
}

func post(h http.Handler, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(body))
	if key != "" {
		r.Header.Set(idempotency.DefaultHeader, key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestReplay(t *testing.T) {
	p := &payment{}
	h := idempotency.Middleware(idempotency.NewMemoryStore())(p)

	first := post(h, "k1", "100")
	second := post(h, "k1", "100")

	if p.calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", p.calls.Load())
	}
	if second.Code != http.StatusCreated || second.Body.String() != `{"charged":100}` {
		t.Fatalf("replay = %d %q", second.Code, second.Body)
	}
	if second.Header().Get("X-Charge") != first.Header().Get("X-Charge") {
		t.Fatal("replay headers differ from original")
	}
	if second.Header().Get(idempotency.ReplayedHeader) != "true" {
		t.Fatal("replay missing Idempotent-Replayed header")
	}
	if first.Header().Get(idempotency.ReplayedHeader) != "" {
		t.Fatal("first response marked as replayed")
	}
}

func TestRejections(t *testing.T) {
	tests := []struct {
		name string
		opts []idempotency.Option
		prep func(h http.Handler) // requests sent before the one checked
		key  string
		body string
		want int
	}{
		{name: "missing key when required", opts: []idempotency.Option{idempotency.WithRequired()}, want: 400},
		{name: "invalid key", key: "has space", want: 400},
		{name: "same key other body", prep: func(h http.Handler) { post(h, "k", "100") }, key: "k", body: "999", want: 422},
		{name: "body too large", opts: []idempotency.Option{idempotency.WithMaxBodyBytes(4)}, key: "k", body: "12345", want: 413},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &payment{}
			h := idempotency.Middleware(idempotency.NewMemoryStore(), tt.opts...)(p)
			if tt.prep != nil {
				tt.prep(h)
			}
			before := p.calls.Load()
			if w := post(h, tt.key, tt.body); w.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.want, w.Body)
			}
			if p.calls.Load() != before {
				t.Fatal("handler ran for a rejected request")
			}
		})
	}
}

func TestPassThrough(t *testing.T) {
	p := &payment{}
	h := idempotency.Middleware(idempotency.NewMemoryStore())(p)
	post(h, "", "1")
	post(h, "", "1")
	r := httptest.NewRequest(http.MethodGet, "/payments", nil)
	r.Header.Set(idempotency.DefaultHeader, "k")
	h.ServeHTTP(httptest.NewRecorder(), r)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if p.calls.Load() != 4 {
		t.Fatalf("handler ran %d times, want 4", p.calls.Load())
	}
}

func TestInFlightConflict(t *testing.T) {
	p := &payment{gate: make(chan struct{})}
	h := idempotency.Middleware(idempotency.NewMemoryStore())(p)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); post(h, "k", "1") }()
	for p.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}

	w := post(h, "k", "1")
	close(p.gate)
	wg.Wait()

	if w.Code != http.StatusConflict || w.Header().Get("Retry-After") == "" {
		t.Fatalf("concurrent retry = %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if p.calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", p.calls.Load())
	}
}

func TestServerErrorReleasesKey(t *testing.T) {
	p := &payment{status: http.StatusBadGateway}
	h := idempotency.Middleware(idempotency.NewMemoryStore())(p)
	post(h, "k", "1")
	p.status = http.StatusCreated
	if w := post(h, "k", "1"); w.Code != http.StatusCreated {
		t.Fatalf("retry after 502 = %d, want 201", w.Code)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", p.calls.Load())
	}
}

func TestClientErrorIsStored(t *testing.T) {
	p := &payment{status: http.StatusPaymentRequired}
	h := idempotency.Middleware(idempotency.NewMemoryStore())(p)
	post(h, "k", "1")
	if w := post(h, "k", "1"); w.Code != http.StatusPaymentRequired {
		t.Fatalf("replay = %d, want 402", w.Code)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", p.calls.Load())
	}
}

func TestPanicReleasesKey(t *testing.T) {
	calls := 0
	h := idempotency.Middleware(idempotency.NewMemoryStore())(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if calls++; calls == 1 {
				panic("boom")
			}
			w.WriteHeader(http.StatusOK)
		}))
	func() {
		defer func() { recover() }()
		post(h, "k", "1")
	}()
	if w := post(h, "k", "1"); w.Code != http.StatusOK || calls != 2 {
		t.Fatalf("retry after panic = %d, calls %d", w.Code, calls)
	}
}

func TestHeadersWithoutWriteAreStored(t *testing.T) {
	h := idempotency.Middleware(idempotency.NewMemoryStore())(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", "/p/1") }))
	post(h, "k", "")
	if w := post(h, "k", ""); w.Code != 200 || w.Header().Get("Location") != "/p/1" {
		t.Fatalf("replay = %d, Location %q", w.Code, w.Header().Get("Location"))
	}
}

// brokenStore fails the operations named in fail.
type brokenStore struct {
	idempotency.Store
	fail string
}

var errDown = errors.New("store down")

func (b brokenStore) Acquire(ctx context.Context, rec idempotency.Record, ttl time.Duration) (idempotency.Record, bool, error) {
	if b.fail == "acquire" {
		return idempotency.Record{}, false, errDown
	}
	return b.Store.Acquire(ctx, rec, ttl)
}

func (b brokenStore) Complete(ctx context.Context, k idempotency.Key, l idempotency.Lease, res []byte, ttl time.Duration) error {
	if b.fail == "complete" {
		return errDown
	}
	return b.Store.Complete(ctx, k, l, res, ttl)
}

func TestStoreDownFailsClosed(t *testing.T) {
	p := &payment{}
	h := idempotency.Middleware(brokenStore{idempotency.NewMemoryStore(), "acquire"})(p)
	if w := post(h, "k", "1"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if p.calls.Load() != 0 {
		t.Fatal("handler ran while the store was down")
	}
}

func TestCompleteFailureIsReported(t *testing.T) {
	var reported error
	h := idempotency.Middleware(
		brokenStore{idempotency.NewMemoryStore(), "complete"},
		idempotency.WithReportError(func(_ *http.Request, err error) { reported = err }),
	)(&payment{})
	if w := post(h, "k", "1"); w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want the handler's 201", w.Code)
	}
	if !errors.Is(reported, errDown) {
		t.Fatalf("reported = %v, want errDown", reported)
	}
}

func FuzzDecodeResponse(f *testing.F) {
	f.Add([]byte{1, 0xc9, 0x01, 0})
	f.Add([]byte{1, 0xc8, 0x01, 1, 1, 'a', 1, 1, 'b', 'x'})
	f.Add([]byte{1, 0xc8, 0x01, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Fuzz(func(t *testing.T, b []byte) {
		idempotency.DecodeResponse(b) // must not panic or over-allocate
	})
}
