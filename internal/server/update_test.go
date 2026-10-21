package server

import (
	"errors"
	"net/http"
	"strings"
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

// Échec de l'installation : le bouton refait une demande (un premier échec le
// rendait muet), et la page montre la raison au lieu de « prête ».
func TestRestartFailedRearms(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	s := NewWith("dev", config.Defaults(), "jeton-de-test-0123456789abcdef", 8733, Options{})
	first := s.RestartRequested()
	s.restartOnce.Do(func() { close(s.restart) })
	select {
	case <-first:
	default:
		t.Fatal("première demande non transmise")
	}
	s.mu.Lock()
	s.update = &Update{Version: "9.9.9", Ready: true}
	s.mu.Unlock()
	s.RestartFailed(errors.New("disque plein"))
	second := s.RestartRequested()
	select {
	case <-second:
		t.Fatal("la nouvelle demande est déjà fermée")
	default:
	}
	s.mu.Lock()
	u := *s.update
	s.mu.Unlock()
	if u.Ready || !strings.Contains(u.Error, "disque plein") {
		t.Fatalf("état après échec : %+v", u)
	}
	s.restartOnce.Do(func() { close(s.restart) })
	select {
	case <-second:
	default:
		t.Fatal("seconde demande non transmise")
	}
}
