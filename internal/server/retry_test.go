package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/V3x0rV3x/D3X/internal/config"
)

// Une connexion à V3X ratée sur une panne passagère est retentée seule
// (MasterMan, 22/10/26) ; une clé refusée ne l'est pas.
func TestV3XRetry(t *testing.T) {
	old := v3xRetryEvery
	v3xRetryEvery = 30 * time.Millisecond
	defer func() { v3xRetryEvery = old }()

	for _, tc := range []struct {
		name      string
		firstCode int
		wantOK    bool
	}{
		{"site en panne puis revenu", http.StatusServiceUnavailable, true},
		{"clé refusée", http.StatusUnauthorized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/me" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if calls.Add(1) == 1 || tc.firstCode == http.StatusUnauthorized {
					w.WriteHeader(tc.firstCode)
					io.WriteString(w, `{"error":"x"}`)
					return
				}
				io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
			}))
			defer ts.Close()

			tmp := t.TempDir()
			t.Setenv("AppData", tmp)
			t.Setenv("XDG_CONFIG_HOME", tmp)
			t.Setenv("HOME", tmp)
			cfg := config.Defaults()
			cfg.V3XBase, cfg.V3XKey = ts.URL, "cle"
			s := NewWith("dev", cfg, "jeton-de-test-0123456789abcdef", 8733, Options{})

			ok := func() bool {
				s.mu.Lock()
				defer s.mu.Unlock()
				return s.me != nil && s.v3xErr == ""
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && !ok() {
				time.Sleep(20 * time.Millisecond)
			}
			if ok() != tc.wantOK {
				s.mu.Lock()
				t.Fatalf("connecté : %v (erreur %q, %d appels), attendu %v", s.me != nil, s.v3xErr, calls.Load(), tc.wantOK)
			}
			if !tc.wantOK {
				// Laisse passer plusieurs tours : la clé refusée n'est pas redemandée.
				time.Sleep(200 * time.Millisecond)
				if n := calls.Load(); n != 1 {
					t.Fatalf("clé refusée redemandée %d fois", n)
				}
			}
		})
	}
}
