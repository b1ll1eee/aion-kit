package idempotency_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/b1ll1eee/aion-kit/idempotency"
)

func ExampleMiddleware() {
	charges := 0
	pay := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		charges++
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "charge #%d", charges)
	})

	mux := http.NewServeMux()
	mux.Handle("POST /payments", idempotency.Middleware(idempotency.NewMemoryStore())(pay))

	// The client times out and retries with the same key.
	for range 3 {
		r := httptest.NewRequest("POST", "/payments", strings.NewReader(`{"amount":100}`))
		r.Header.Set("Idempotency-Key", "order-42")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		body, _ := io.ReadAll(w.Body)
		replayed := w.Header().Get("Idempotent-Replayed") == "true"
		fmt.Printf("%d %s replayed=%v\n", w.Code, body, replayed)
	}
	// Output:
	// 201 charge #1 replayed=false
	// 201 charge #1 replayed=true
	// 201 charge #1 replayed=true
}

func ExampleGuard() {
	g := idempotency.NewGuard(idempotency.NewMemoryStore())
	ctx := context.Background()
	key, _ := idempotency.ParseKey("sms-batch-7")
	fp := idempotency.FingerprintOf([]byte("+66812345678"), []byte("Your OTP is 1234"))

	send := func() string {
		att, err := g.Begin(ctx, key, fp)
		if err != nil {
			return err.Error()
		}
		if res, ok := att.Replay(); ok {
			return "skipped, already sent: " + string(res)
		}
		// ... call the SMS gateway ...
		g.Complete(ctx, att, []byte("msg-id-991"))
		return "sent"
	}

	fmt.Println(send())
	fmt.Println(send())
	// Output:
	// sent
	// skipped, already sent: msg-id-991
}
