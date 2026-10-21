package server

import (
	"net/http"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/config"
)

// Fenêtre ratée (Luci, 21/10/26) : une page chargée mais pas à l'écran est
// signalée, puis priée de se fermer dès qu'une autre page s'affiche.
func TestPingShown(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	cfg := config.Defaults()
	cfg.CheckUpdates = false
	s := NewWith("dev", cfg, "jeton-de-test-0123456789abcdef", 8733, Options{})

	if code, _, _ := call(t, s, http.MethodGet, "/ui/ping?id=premiere&shown=0", nil); code != http.StatusNoContent {
		t.Fatalf("première page : %d", code)
	}
	if !s.PageHidden() || s.PageShown() {
		t.Fatal("une page chargée mais pas affichée : PageHidden attendu")
	}
	if code, _, _ := call(t, s, http.MethodGet, "/ui/ping?id=seconde&shown=1", nil); code != http.StatusNoContent {
		t.Fatalf("seconde page : %d", code)
	}
	if s.PageHidden() || !s.PageShown() {
		t.Fatal("une page affichée : PageShown attendu")
	}
	code, out, body := call(t, s, http.MethodGet, "/ui/ping?id=premiere&shown=0", nil)
	if code != http.StatusOK || out["close"] != true {
		t.Fatalf("la page jamais affichée doit se fermer : %d %s", code, body)
	}
	// Une page qui s'est affichée une fois (puis réduite) ne reçoit jamais
	// l'ordre de se fermer.
	if code, _, _ := call(t, s, http.MethodGet, "/ui/ping?id=seconde&shown=0", nil); code != http.StatusNoContent {
		t.Fatalf("page réduite : %d", code)
	}
}
