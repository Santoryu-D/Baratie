package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/V3x0rV3x/D3X/internal/config"
)

// Bouton « Vérifier » : en version de développement, la route répond sans
// interroger GitHub ; juste après une recherche, elle renvoie l'état connu sans
// nouvel appel (une recherche par minute au plus).
func TestUpdateCheckRoute(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	s := NewWith("dev", config.Defaults(), "jeton-de-test-0123456789abcdef", 8733, Options{})
	code, out, body := call(t, s, http.MethodPost, "/ui/update/check", nil)
	if code != http.StatusOK || out["version"] != "dev" || out["latest"] != nil {
		t.Fatalf("version de développement : %d %s", code, body)
	}

	// Un second serveur déjà en 1.1.1 (changer s.Version pendant que sa boucle
	// de mise à jour le lit est une course), sans recherche automatique.
	cfg := config.Defaults()
	cfg.CheckUpdates = false
	s2 := NewWith("1.1.1", cfg, "jeton-de-test-0123456789abcdef", 8733, Options{})
	s2.lastUpdateCheck.Store(time.Now().UnixNano())
	code, out, body = call(t, s2, http.MethodPost, "/ui/update/check", nil)
	if code != http.StatusOK || out["version"] != "1.1.1" || out["latest"] != nil || out["update"] != nil {
		t.Fatalf("recherche toute récente : %d %s", code, body)
	}
}
