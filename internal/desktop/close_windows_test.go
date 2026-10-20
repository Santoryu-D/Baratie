//go:build windows

package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Essai réel, à la demande seulement (D3X_TEST_EDGE=1) : ouvre une fenêtre
// d'application avec un profil jetable, puis vérifie que CloseWindow arrête
// ses processus, et eux seuls.
func TestCloseWindowRealBrowser(t *testing.T) {
	if os.Getenv("D3X_TEST_EDGE") != "1" {
		t.Skip("essai réel : D3X_TEST_EDGE=1")
	}
	dir := filepath.Join(t.TempDir(), "fenetre-essai")
	if OpenWindow("about:blank", dir) == nil {
		t.Skip("aucun navigateur")
	}
	time.Sleep(4 * time.Second)
	n := CloseWindow(dir)
	if n == 0 {
		t.Fatal("aucun processus arrêté")
	}
	time.Sleep(2 * time.Second)
	if again := CloseWindow(dir); again != 0 {
		t.Fatalf("%d processus encore là", again)
	}
	t.Logf("%d processus arrêtés", n)
}

// Essai réel (D3X_TEST_EDGE=1) : une fenêtre rouverte aussitôt après
// CloseWindow sur le même profil charge bien sa page (elle restait vide quand
// le navigateur arrêté n'avait pas fini de partir, Luci, 21/10/26).
func TestReopenRealBrowser(t *testing.T) {
	if os.Getenv("D3X_TEST_EDGE") != "1" {
		t.Skip("essai réel : D3X_TEST_EDGE=1")
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			hits.Add(1)
		}
		io.WriteString(w, "<!doctype html><title>essai</title>ok")
	}))
	defer srv.Close()
	wait := func(n int32) bool {
		for i := 0; i < 100; i++ {
			if hits.Load() >= n {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}
	dir := filepath.Join(t.TempDir(), "fenetre-essai")
	if OpenWindow(srv.URL+"/", dir) == nil {
		t.Skip("aucun navigateur")
	}
	if !wait(1) {
		t.Fatal("première fenêtre : page jamais chargée")
	}
	time.Sleep(time.Second)
	if CloseWindow(dir) == 0 {
		t.Fatal("aucun processus arrêté")
	}
	OpenWindow(srv.URL+"/", dir)
	ok := wait(2)
	CloseWindow(dir)
	if !ok {
		t.Fatal("fenêtre rouverte aussitôt : page jamais chargée")
	}
}
