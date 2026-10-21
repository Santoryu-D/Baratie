package v3x

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Recherche de jeux (Nayr, 22/10/26) : relayée vers /api/d3x/igdb/*, clé en
// en-tête, route inconnue refusée ; le jeu choisi part avec la fiche.
func TestIGDBRelayAndFiche(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	var fiche map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/d3x/fiche":
			json.NewDecoder(r.Body).Decode(&fiche)
			w.Write([]byte(`{"title":"Jeu (2024)"}`))
		default:
			gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("q")
			w.Write([]byte(`{"results":[],"configured":true}`))
		}
	}))
	defer ts.Close()
	c := New(ts.URL, "cle-secrete-123", "D3X/test")
	ctx := context.Background()
	if _, err := c.IGDB(ctx, "search", url.Values{"q": {"Baldur's Gate 3"}}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/d3x/igdb/search" || gotQuery != "Baldur's Gate 3" || gotAuth != "Bearer cle-secrete-123" {
		t.Fatalf("chemin %q, recherche %q, en-tête %q", gotPath, gotQuery, gotAuth)
	}
	if _, err := c.IGDB(ctx, "../torrents", nil); err == nil {
		t.Fatal("route inconnue acceptée")
	}
	id := int64(119171)
	if _, err := c.Fiche(ctx, FicheRequest{ReleaseName: "Jeu.2024.PC", IgdbID: &id}); err != nil {
		t.Fatal(err)
	}
	if v, ok := fiche["igdbId"].(float64); !ok || int64(v) != id {
		t.Fatalf("igdbId envoyé : %v", fiche["igdbId"])
	}
	// Sans jeu choisi, le champ n'est pas envoyé du tout.
	fiche = nil
	if _, err := c.Fiche(ctx, FicheRequest{ReleaseName: "Film.2024.1080p"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fiche["igdbId"]; ok {
		t.Fatalf("igdbId envoyé sans jeu : %v", fiche)
	}
}
