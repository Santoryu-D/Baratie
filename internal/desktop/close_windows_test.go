//go:build windows

package desktop

import (
	"os"
	"path/filepath"
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
