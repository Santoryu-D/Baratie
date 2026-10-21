package discogs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Recherche et pochette : jeton en en-tête (jamais dans l'adresse), pochette
// principale préférée, images d'ailleurs que Discogs refusées, jeton refusé dit
// en clair.
func TestDiscogs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Discogs token=jeton-ok" || strings.Contains(r.URL.RawQuery, "jeton") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/database/search":
			json.NewEncoder(w).Encode(map[string]interface{}{"results": []map[string]interface{}{
				{"id": 123, "title": "Artiste - Album", "year": "1999", "format": []string{"CD", "Album"}, "label": []string{"Label", "Label"}, "thumb": "https://i.discogs.com/t.jpg"},
				{"id": 124, "title": "Autre", "thumb": "https://evil.example/t.jpg"},
			}})
		case "/releases/123":
			json.NewEncoder(w).Encode(map[string]interface{}{"images": []map[string]string{
				{"type": "secondary", "uri": "https://i.discogs.com/dos.jpg"},
				{"type": "primary", "uri": "https://i.discogs.com/face.jpg"},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	old := Base
	Base = ts.URL
	defer func() { Base = old }()

	c := New("test", "jeton-ok")
	res, err := c.Search(context.Background(), "artiste album")
	if err != nil || len(res) != 2 || res[0].ID != 123 || res[0].Thumb == "" || res[1].Thumb != "" || len(res[0].Label) != 1 {
		t.Fatalf("Search = %+v, %v", res, err)
	}
	if u, err := c.CoverURL(context.Background(), 123); err != nil || u != "https://i.discogs.com/face.jpg" {
		t.Fatalf("CoverURL = %q, %v", u, err)
	}
	if _, err := c.Download(context.Background(), "https://evil.example/x.jpg"); err == nil {
		t.Fatal("image hors de Discogs acceptée")
	}
	if _, err := New("test", "mauvais").Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "jeton") {
		t.Fatalf("jeton refusé : %v", err)
	}
	if _, err := New("test", "").Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "Réglages") {
		t.Fatalf("sans jeton : %v", err)
	}
}
