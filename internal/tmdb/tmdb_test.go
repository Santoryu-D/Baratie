package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T, wantV4 bool) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ok := q.Get("api_key") == "cle3"
		if wantV4 {
			ok = r.Header.Get("Authorization") == "Bearer eyJjeton" && q.Get("api_key") == ""
		}
		if !ok {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/search/movie":
			res := []map[string]interface{}{}
			if strings.EqualFold(q.Get("query"), "inception") && (q.Get("year") == "" || q.Get("year") == "2010") {
				res = append(res, map[string]interface{}{"id": 27205, "title": "Inception", "original_title": "Inception", "release_date": "2010-07-15", "poster_path": "/p.jpg"})
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"results": res})
		case r.URL.Path == "/movie/27205":
			title := "Inception (fr)"
			if q.Get("language") == "en-US" {
				title = "Inception"
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"id": 27205, "title": title, "release_date": "2010-07-15", "imdb_id": "tt1375666"})
		case r.URL.Path == "/find/tt1375666":
			json.NewEncoder(w).Encode(map[string]interface{}{"movie_results": []map[string]interface{}{{"id": 27205, "title": "Inception", "release_date": "2010-07-15"}}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	old := Base
	Base = srv.URL
	t.Cleanup(func() { Base = old })
}

func TestSearchDetailsLookup(t *testing.T) {
	fake(t, false)
	c := New("cle3")
	ctx := context.Background()
	ms, err := c.Search(ctx, "Inception", "2009") // mauvaise année : réessai sans
	if err != nil || len(ms) != 1 || ms[0].ID != 27205 || ms[0].Year != "2010" || !strings.HasSuffix(ms[0].Poster, "/p.jpg") {
		t.Fatalf("%+v %v", ms, err)
	}
	m, err := c.Details(ctx, 27205)
	if err != nil || m.IMDB != 1375666 || m.TitleEN != "Inception" || m.Title != "Inception (fr)" {
		t.Fatalf("%+v %v", m, err)
	}
	for _, ref := range []string{"https://www.themoviedb.org/movie/27205-inception", "27205", "https://www.imdb.com/title/tt1375666/", "tt1375666"} {
		ms, err := c.Lookup(ctx, ref)
		if err != nil || len(ms) != 1 || ms[0].ID != 27205 {
			t.Fatalf("%s : %+v %v", ref, ms, err)
		}
	}
	_, err = New("mauvaise").Search(ctx, "Inception", "")
	if err == nil || strings.Contains(err.Error(), "mauvaise") {
		t.Fatalf("clé refusée : %v", err)
	}
}

func TestV4(t *testing.T) {
	fake(t, true)
	if _, err := New("eyJjeton").Details(context.Background(), 27205); err != nil {
		t.Fatal(err)
	}
}
