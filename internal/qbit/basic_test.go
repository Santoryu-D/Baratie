package qbit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Seedbox derrière une identification HTTP du serveur (calcifer, 23/10/26) :
// le pseudo et le mot de passe des champs servent aussi à cette fenêtre.
func TestBasicAuthProxy(t *testing.T) {
	ctx := context.Background()
	var challenged, logins atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "calc" || p != "secret" {
			challenged.Add(1)
			w.Header().Set("WWW-Authenticate", `Basic realm="seedbox"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/qbittorrent/api/v2/auth/login":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "username=calc") {
				io.WriteString(w, "Fails.")
				return
			}
			logins.Add(1)
			io.WriteString(w, "Ok.")
		case "/qbittorrent/api/v2/app/version":
			io.WriteString(w, "v5.1.2")
		case "/qbittorrent/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.4")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()

	c, _ := New(Options{URL: s.URL + "/qbittorrent", User: "calc", Pass: "secret"})
	if v, err := c.Version(ctx); err != nil || v != "v5.1.2" {
		t.Fatalf("derrière l'identification : %q, %v", v, err)
	}
	if logins.Load() != 1 {
		t.Fatalf("connexion à qBittorrent : %d, le formulaire doit être rejoué avec son corps", logins.Load())
	}
	// Une fois acceptée, l'identification part d'emblée : plus de 401.
	before := challenged.Load()
	if _, err := c.Version(ctx); err != nil {
		t.Fatal(err)
	}
	if challenged.Load() != before {
		t.Fatal("l'identification aurait dû partir d'emblée")
	}

	// Mauvais mot de passe : refus clair, pas de boucle.
	bad, _ := New(Options{URL: s.URL + "/qbittorrent", User: "calc", Pass: "faux"})
	if _, err := bad.Version(ctx); err == nil || !strings.Contains(err.Error(), "identification du serveur") {
		t.Fatalf("mot de passe faux : %v", err)
	}

	// Identifiants dans l'adresse (l'astuce d'avant) : toujours valables.
	in := strings.Replace(s.URL, "http://", "http://calc:secret@", 1) + "/qbittorrent"
	old, _ := New(Options{URL: in, User: "calc", Pass: "secret"})
	if v, err := old.Version(ctx); err != nil || v != "v5.1.2" {
		t.Fatalf("identifiants dans l'adresse : %q, %v", v, err)
	}
}
