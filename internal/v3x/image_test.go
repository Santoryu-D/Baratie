package v3x

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Image du PC : envoyée comme la page d'upload du site (champ « file », type
// image/… lu dans les octets), avec la clé en en-tête ; les refus du site
// reviennent en clair.
func TestUploadImage(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/upload" || r.Header.Get("Authorization") != "Bearer cle-de-test-123456" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Errorf("champ file absent : %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.Close()
		if ct := h.Header.Get("Content-Type"); ct != "image/png" || h.Filename != "cover.png" {
			t.Errorf("type %q, nom %q", ct, h.Filename)
		}
		json.NewEncoder(w).Encode(map[string]string{"url": "https://i.v3x.club/abc.png"})
	}))
	defer ts.Close()
	c := New(ts.URL, "cle-de-test-123456", "D3X/test")
	u, err := c.UploadImage(context.Background(), "cover.png", png)
	if err != nil || u != "https://i.v3x.club/abc.png" {
		t.Fatalf("UploadImage = %q, %v", u, err)
	}

	bad := New(ts.URL, "autre-cle-0000000000", "D3X/test")
	if _, err := bad.UploadImage(context.Background(), "cover.png", png); err == nil || !strings.Contains(err.Error(), "Upload") {
		t.Fatalf("clé refusée : %v", err)
	}
}
