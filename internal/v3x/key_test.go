package v3x

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// La clé ne voyage jamais dans une adresse, et une erreur réseau ne la
// recopie pas.
func TestKeyNeverInURL(t *testing.T) {
	var gotAuth, gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotQuery = r.Header.Get("Authorization"), r.URL.RawQuery
		w.Write([]byte("d4:infod4:name1:xee"))
	}))
	c := New(ts.URL, "cle-secrete-123", "D3X/test")
	if _, err := c.DownloadTorrent(context.Background(), nil, "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer cle-secrete-123" || strings.Contains(gotQuery, "cle-secrete") || strings.Contains(gotQuery, "apikey") {
		t.Fatalf("en-tête %q, adresse %q", gotAuth, gotQuery)
	}
	ts.Close()
	// Serveur fermé : erreur réseau, sans l'adresse ni la clé.
	_, err := c.DownloadTorrent(context.Background(), nil, "11111111-1111-1111-1111-111111111111")
	if err == nil || strings.Contains(err.Error(), "cle-secrete") || strings.Contains(err.Error(), "torznab") {
		t.Fatalf("erreur : %v", err)
	}
}
