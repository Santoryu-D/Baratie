package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

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

// Sortie de veille (karat, 24/10/26) : une page restée muette pendant la
// veille n'est pas partie ; une page qui a dit au revoir, si.
func TestResumedGivesPageTime(t *testing.T) {
	s := &Server{}
	s.Resumed() // page jamais venue : rien à faire
	if s.PageSeen() {
		t.Fatal("Resumed ne doit pas inventer une page")
	}
	old := time.Now().Add(-10 * time.Minute).UnixNano()
	s.lastSeen.Store(old)
	if !s.PageGone() {
		t.Fatal("10 min de silence : page partie attendue")
	}
	s.Resumed()
	if s.PageGone() {
		t.Fatal("au réveil, la page doit avoir le temps de revenir")
	}

	s.lastSeen.Store(old)
	s.byeAt.Store(old + int64(time.Second))
	s.Resumed()
	if !s.PageGone() {
		t.Fatal("une page fermée avant la veille reste partie")
	}
}

// Lot en cours (karat, 24/10/26) : une page qui se tait pendant son lot
// (fenêtre endormie, écran éteint) n'est pas partie ; à la fin du lot, après
// 12 h, ou quand la page du lot dit au revoir, si.
func TestLotKeepsPageAlive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	cfg := config.Defaults()
	cfg.CheckUpdates = false
	s := NewWith("dev", cfg, "jeton-de-test-0123456789abcdef", 8733, Options{})
	quiet := func(d time.Duration) { s.lastSeen.Store(time.Now().Add(-d).UnixNano()) }

	if code, _, _ := call(t, s, http.MethodGet, "/ui/ping?id=lot&shown=1&lot=1", nil); code != http.StatusNoContent {
		t.Fatalf("ping du lot : %d", code)
	}
	if !s.LotRunning() || !s.LotAdvancing() {
		t.Fatal("lot signalé : LotRunning et LotAdvancing attendus")
	}
	quiet(time.Hour)
	if s.PageGone() {
		t.Fatal("lot en cours : une heure de silence n'arrête pas D3X")
	}
	if s.LotAdvancing() {
		t.Fatal("page muette : son lot n'avance plus, la veille doit revenir")
	}
	s.pagesMu.Lock()
	s.lotPages["lot"] = time.Now().Add(-13 * time.Hour).UnixNano()
	s.pagesMu.Unlock()
	quiet(13 * time.Hour)
	if !s.PageGone() {
		t.Fatal("13 h de silence : page partie attendue")
	}

	// Fin du lot : le silence ordinaire revient.
	call(t, s, http.MethodGet, "/ui/ping?id=lot&shown=1&lot=1", nil)
	call(t, s, http.MethodGet, "/ui/ping?id=lot&shown=1&lot=0", nil)
	quiet(10 * time.Minute)
	if s.LotRunning() || !s.PageGone() {
		t.Fatal("lot fini : 10 min de silence = page partie")
	}

	// Au revoir d'une autre page : le lot continue ; celui de la page du lot : fini.
	call(t, s, http.MethodGet, "/ui/ping?id=lot&shown=1&lot=1", nil)
	bye := func(id string) {
		if code, _, _ := call(t, s, http.MethodPost, "/ui/bye", map[string]string{"id": id}); code != http.StatusNoContent {
			t.Fatalf("au revoir : %d", code)
		}
		t0 := time.Now().Add(-20 * time.Second).UnixNano()
		s.lastSeen.Store(t0)
		s.byeAt.Store(t0 + int64(time.Second))
	}
	bye("autre")
	if s.PageGone() {
		t.Fatal("une autre page fermée : le lot continue")
	}
	bye("lot")
	if s.LotRunning() || !s.PageGone() {
		t.Fatal("page du lot fermée : page partie attendue")
	}
	if !strings.Contains(s.GoneWhy(), "au revoir") {
		t.Fatalf("raison : %q", s.GoneWhy())
	}
}
