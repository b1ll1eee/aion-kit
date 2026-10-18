// Package idempotency makes unsafe operations safe to retry.
//
// A client sends an Idempotency-Key header with a request. The first request
// with that key runs the handler; every retry with the same key and the same
// payload gets the stored response back without running the handler again.
// A retry that arrives while the first request is still running is rejected
// with 409, and reusing a key for a different payload is rejected with 422.
//
//	store := idempotency.NewMemoryStore()
//	mux.Handle("POST /payments", idempotency.Middleware(store)(payHandler))
//
// The package is split into a transport-agnostic core and adapters:
//
//   - [Key], [Fingerprint] and [Record] are the domain values.
//   - [Guard] runs the begin / complete / abort protocol against a [Store].
//   - [Store] is the port. [MemoryStore] ships here; networked stores live in
//     separate modules (for example github.com/b1ll1eee/aion-kit/redisstore)
//     so this package depends on the standard library only.
//   - [Middleware] adapts the Guard to net/http.
//
// Store implementations should pass the contract tests in the
// idempotencytest package.
package idempotency
