package v3x

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fiche retirée (issue GitHub #1) : sans fiche en ligne, le site renvoie
// « removed » ; D3X en fait une correspondance « gone », jamais un doublon, avec
// le motif seulement quand c'est la fiche du membre.
func TestMatchBatchGone(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{"key": "a", "match": nil, "removed": map[string]interface{}{"name": "Film.2020.1080p", "status": "removed", "own": false, "reason": nil}},
				{"key": "b", "match": nil, "removed": map[string]interface{}{"name": "Film.2021.1080p", "status": "trumped", "own": true, "reason": "remplacée par la REMUX"}},
				{"key": "c", "match": nil},
			},
			"reseedPrime": 500,
		})
	}))
	defer ts.Close()
	c := New(ts.URL, "cle-de-test-123456", "D3X/test")
	res, err := c.MatchBatch(context.Background(), nil, []MatchItem{{Key: "a"}, {Key: "b"}, {Key: "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if m := res["a"]; m == nil || m.Kind != "gone" || m.Gone == nil || strings.Contains(m.Gone.Text(), "motif") {
		t.Fatalf("a : %+v", m)
	}
	if m := res["b"]; m == nil || !strings.Contains(m.Gone.Text(), "remplacée par une meilleure version") || !strings.Contains(m.Gone.Text(), "remplacée par la REMUX") {
		t.Fatalf("b : %+v / %q", m, m.Gone.Text())
	}
	if res["c"] != nil {
		t.Fatalf("c : %+v", res["c"])
	}
}
