package idempotency

import (
	"log/slog"
	"net/http"
	"time"
)

// Defaults used when no option overrides them.
const (
	DefaultHeader       = "Idempotency-Key"
	DefaultRetention    = 24 * time.Hour
	DefaultLockTimeout  = time.Minute
	DefaultMaxBodyBytes = 1 << 20
)

// ReplayedHeader is set to "true" on responses served from the store.
const ReplayedHeader = "Idempotent-Replayed"

// FingerprintFunc derives the fingerprint of a request. body is the full
// request body, already read.
type FingerprintFunc func(r *http.Request, body []byte) Fingerprint

// ErrorHandler writes the response for a request rejected before the handler
// ran, for example with [ErrInFlight] or [ErrMismatch].
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// Option configures a [Guard] or [Middleware].
type Option func(*config)

type config struct {
	header       string
	required     bool
	methods      []string
	retention    time.Duration
	lockTimeout  time.Duration
	maxBodyBytes int64
	fingerprint  FingerprintFunc
	onError      ErrorHandler
	report       func(r *http.Request, err error)
}

func newConfig(opts []Option) config {
	c := config{
		header:       DefaultHeader,
		methods:      []string{http.MethodPost, http.MethodPatch},
		retention:    DefaultRetention,
		lockTimeout:  DefaultLockTimeout,
		maxBodyBytes: DefaultMaxBodyBytes,
		fingerprint:  DefaultFingerprint,
		onError:      DefaultErrorHandler,
		report:       logReport,
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// WithHeader sets the request header that carries the key.
func WithHeader(name string) Option { return func(c *config) { c.header = name } }

// WithRequired rejects requests without a key with 400 instead of passing
// them through unprotected.
func WithRequired() Option { return func(c *config) { c.required = true } }

// WithMethods sets which methods are protected. The default is POST and
// PATCH; other methods pass through untouched.
func WithMethods(methods ...string) Option {
	return func(c *config) { c.methods = methods }
}

// WithRetention sets how long a completed result is replayed.
func WithRetention(d time.Duration) Option { return func(c *config) { c.retention = d } }

// WithLockTimeout sets how long an in-flight request holds its key. It must
// be longer than the slowest handler; when it expires a retry may run the
// handler again.
func WithLockTimeout(d time.Duration) Option { return func(c *config) { c.lockTimeout = d } }

// WithMaxBodyBytes caps the request body read for fingerprinting. Larger
// bodies are rejected with 413.
func WithMaxBodyBytes(n int64) Option { return func(c *config) { c.maxBodyBytes = n } }

// WithFingerprint replaces [DefaultFingerprint], for example to ignore a
// field that changes on every retry.
func WithFingerprint(f FingerprintFunc) Option { return func(c *config) { c.fingerprint = f } }

// WithErrorHandler replaces [DefaultErrorHandler].
func WithErrorHandler(h ErrorHandler) Option { return func(c *config) { c.onError = h } }

// WithReportError receives errors that happen after the response was sent,
// such as a failed write of the result to the store. These cannot reach the
// client, but they mean a retry may run the handler again, so they should be
// alerted on. The default logs them with slog.
func WithReportError(f func(r *http.Request, err error)) Option {
	return func(c *config) { c.report = f }
}

// DefaultFingerprint hashes the method, request URI and body.
func DefaultFingerprint(r *http.Request, body []byte) Fingerprint {
	return FingerprintOf([]byte(r.Method), []byte(r.URL.RequestURI()), body)
}

func logReport(r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "idempotency: result not stored", "err", err)
}
