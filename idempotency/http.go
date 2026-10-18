package idempotency

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
)

// ErrBodyTooLarge means the request body exceeded [WithMaxBodyBytes].
var ErrBodyTooLarge = errors.New("idempotency: request body too large")

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// Middleware protects handlers with a [Guard] over store. Responses with a
// status below 500 are stored and replayed; 5xx responses and panics release
// the key so the client can retry.
func Middleware(store Store, opts ...Option) func(http.Handler) http.Handler {
	g := NewGuard(store, opts...)
	return func(next http.Handler) http.Handler {
		return &handler{g: g, cfg: &g.cfg, next: next}
	}
}

type handler struct {
	g    *Guard
	cfg  *config
	next http.Handler
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !slices.Contains(h.cfg.methods, r.Method) {
		h.next.ServeHTTP(w, r)
		return
	}
	raw := r.Header.Get(h.cfg.header)
	if raw == "" {
		if h.cfg.required {
			h.cfg.onError(w, r, ErrMissingKey)
			return
		}
		h.next.ServeHTTP(w, r)
		return
	}
	key, err := ParseKey(raw)
	if err != nil {
		h.cfg.onError(w, r, err)
		return
	}

	body := bufPool.Get().(*bytes.Buffer)
	defer putBuf(body)
	if err := h.readBody(r, body); err != nil {
		h.cfg.onError(w, r, err)
		return
	}

	att, err := h.g.Begin(r.Context(), key, h.cfg.fingerprint(r, body.Bytes()))
	if err != nil {
		h.cfg.onError(w, r, err)
		return
	}
	if res, ok := att.Replay(); ok {
		h.replay(w, r, res)
		return
	}
	h.run(w, r, att)
}

// readBody buffers the request body into buf and points r.Body at it.
func (h *handler) readBody(r *http.Request, buf *bytes.Buffer) error {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	_, err := buf.ReadFrom(io.LimitReader(r.Body, h.cfg.maxBodyBytes+1))
	r.Body.Close()
	if err != nil {
		return err
	}
	if int64(buf.Len()) > h.cfg.maxBodyBytes {
		return ErrBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
	return nil
}

func (h *handler) run(w http.ResponseWriter, r *http.Request, att Attempt) {
	// The client may hang up mid-request; the result must still be stored.
	ctx := context.WithoutCancel(r.Context())
	rec := newRecorder(w)
	defer putBuf(rec.body)

	finished := false
	defer func() {
		if !finished { // handler panicked
			if err := h.g.Abort(ctx, att); err != nil {
				h.cfg.report(r, err)
			}
		}
	}()
	h.next.ServeHTTP(rec, r)
	finished = true

	if rec.status() >= 500 {
		if err := h.g.Abort(ctx, att); err != nil {
			h.cfg.report(r, err)
		}
		return
	}
	if err := h.g.Complete(ctx, att, rec.encode()); err != nil {
		h.cfg.report(r, err)
	}
}

func (h *handler) replay(w http.ResponseWriter, r *http.Request, res []byte) {
	resp, err := decodeResponse(res)
	if err != nil {
		h.cfg.report(r, err)
		h.cfg.onError(w, r, err)
		return
	}
	dst := w.Header()
	for k, v := range resp.header {
		dst[k] = v
	}
	dst.Set(ReplayedHeader, "true")
	w.WriteHeader(resp.status)
	w.Write(resp.body)
}

// StatusCode maps errors returned by this package to an HTTP status.
func StatusCode(err error) int {
	switch {
	case errors.Is(err, ErrInvalidKey), errors.Is(err, ErrMissingKey):
		return http.StatusBadRequest
	case errors.Is(err, ErrInFlight):
		return http.StatusConflict
	case errors.Is(err, ErrMismatch):
		return http.StatusUnprocessableEntity
	case errors.Is(err, ErrBodyTooLarge):
		return http.StatusRequestEntityTooLarge
	}
	// Store unreachable: fail closed rather than risk a double charge.
	return http.StatusServiceUnavailable
}

// DefaultErrorHandler writes a plain-text error with [StatusCode]. For
// [ErrInFlight] it also sets Retry-After.
func DefaultErrorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	code := StatusCode(err)
	msg := err.Error()
	if code == http.StatusServiceUnavailable {
		msg = "idempotency store unavailable"
	}
	if code == http.StatusConflict {
		w.Header().Set("Retry-After", "1")
	}
	http.Error(w, msg, code)
}

func putBuf(b *bytes.Buffer) {
	if b.Cap() > 4*DefaultMaxBodyBytes {
		return // let oversized buffers go to the GC
	}
	b.Reset()
	bufPool.Put(b)
}
