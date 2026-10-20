package v3x

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfterMargin(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "0")
	if d := retryAfter(h, 0); d < time.Second {
		t.Errorf("Retry-After: 0 doit laisser une seconde de marge, pas %v", d)
	}
	if d := retryAfter(http.Header{}, 2); d != 8*time.Second {
		t.Errorf("sans en-tête, 3e essai : %v, 8 s attendues", d)
	}
}

func TestRetryOnlyInBatch(t *testing.T) {
	var calls int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"username":"x"}`)
	}))
	defer s.Close()
	c := New(s.URL, "k", "t")
	ctx := context.Background()

	// Action cliquée : l'erreur remonte tout de suite.
	if _, err := c.Me(ctx); err == nil {
		t.Fatal("un 429 hors lot doit remonter")
	}
	// En lot : attente puis nouvel essai.
	atomic.StoreInt32(&calls, 0)
	var waited bool
	err := c.doJSON(ctx, &Retry{OnWait: func(d time.Duration) { waited = waited || d > 0 }}, http.MethodGet, "/api/me", nil, nil)
	if err != nil || !waited || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("lot : err=%v attente=%v appels=%d", err, waited, calls)
	}
}

func TestHugeRetryAfterGivesUp(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer s.Close()
	c := New(s.URL, "k", "t")
	start := time.Now()
	if err := c.doJSON(context.Background(), &Retry{}, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("une heure de pause demandée : abandon attendu")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("aucune attente inutile au-delà du plafond")
	}
}
