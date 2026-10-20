package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMB sert des éditions inventées comme l'API de MusicBrainz.
type fakeMB struct {
	mu       sync.Mutex
	releases map[string]map[string]interface{}
	queries  []string
	busy     int // réponses 503 à renvoyer d'abord
	ua       string
}

func edition(id, group, title, artist, date, format string, lengths ...int) map[string]interface{} {
	var tracks []interface{}
	for i, l := range lengths {
		tracks = append(tracks, map[string]interface{}{"position": i + 1, "number": fmt.Sprint(i + 1), "title": fmt.Sprintf("Piste %d", i+1), "length": l})
	}
	return map[string]interface{}{
		"id": id, "title": title, "date": date, "country": "FR", "status": "Official", "score": 100,
		"artist-credit":     []interface{}{map[string]interface{}{"name": artist, "joinphrase": ""}},
		"release-group":     map[string]interface{}{"id": group, "primary-type": "Album"},
		"label-info":        []interface{}{map[string]interface{}{"catalog-number": "CAT-1", "label": map[string]interface{}{"name": "Label Démo"}}},
		"media":             []interface{}{map[string]interface{}{"format": format, "position": 1, "track-count": len(lengths), "tracks": tracks}},
		"cover-art-archive": map[string]interface{}{"front": true},
	}
}

func (f *fakeMB) server(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.ua = r.Header.Get("User-Agent")
		if f.busy > 0 {
			f.busy--
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch {
		case r.URL.Path == "/release":
			q := r.URL.Query().Get("query")
			f.queries = append(f.queries, q)
			var list []interface{}
			for _, rel := range f.releases {
				if strings.Contains(strings.ToLower(q), strings.ToLower(rel["title"].(string))) {
					// La recherche ne donne pas les pistes, seulement leur nombre.
					cp := map[string]interface{}{}
					for k, v := range rel {
						cp[k] = v
					}
					m := rel["media"].([]interface{})[0].(map[string]interface{})
					cp["media"] = []interface{}{map[string]interface{}{"format": m["format"], "track-count": m["track-count"]}}
					cp["track-count"] = m["track-count"]
					list = append(list, cp)
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"releases": list})
		case strings.HasPrefix(r.URL.Path, "/release/"):
			rel := f.releases[strings.TrimPrefix(r.URL.Path, "/release/")]
			if rel == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(rel)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	old := Base
	Base = ts.URL
	t.Cleanup(func() { Base = old })
}

func client() *Client {
	c := New("test")
	c.gap = 0
	return c
}

const (
	cd  = "11111111-1111-1111-1111-111111111111"
	web = "22222222-2222-2222-2222-222222222222"
	rg  = "33333333-3333-3333-3333-333333333333"
	oth = "44444444-4444-4444-4444-444444444444"
)

func TestLookupAndSearch(t *testing.T) {
	f := &fakeMB{releases: map[string]map[string]interface{}{cd: edition(cd, rg, "Le Disque", "Artiste", "2015-03-01", "CD", 200000, 180500)}}
	f.server(t)
	c := client()
	r, err := c.Lookup(context.Background(), strings.ToUpper(cd))
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Le Disque" || r.Artist != "Artiste" || r.Year() != "2015" || r.Label != "Label Démo" || r.Catalog != "CAT-1" || r.TrackCount != 2 || len(r.Tracks) != 2 || r.Tracks[1].Length != 180.5 || !r.CoverArchive || r.Group != rg {
		t.Fatalf("%+v", r)
	}
	f.mu.Lock()
	ua := f.ua
	f.mu.Unlock()
	if !strings.HasPrefix(ua, "D3X/test (") {
		t.Fatalf("User-Agent %q", ua)
	}
	if _, err := c.Lookup(context.Background(), oth); err != ErrNotFound {
		t.Fatalf("inconnue : %v", err)
	}
	list, err := c.Search(context.Background(), `Art"iste`, "Le Disque", 10)
	if err != nil || len(list) != 1 || list[0].TrackCount != 2 || list[0].Tracks != nil {
		t.Fatalf("%v %+v", err, list)
	}
	f.mu.Lock()
	q0 := f.queries[0]
	f.mu.Unlock()
	if q := q0; q != `release:"Le Disque" AND artist:"Art\"iste"` {
		t.Fatalf("requête %q", q)
	}
}

// MusicBrainz surchargé (503) : D3X ralentit et réessaie.
func TestBusy(t *testing.T) {
	f := &fakeMB{busy: 2, releases: map[string]map[string]interface{}{cd: edition(cd, rg, "X", "Y", "2015", "CD", 1000)}}
	f.server(t)
	c := client()
	c.gap = 5 * time.Millisecond
	if _, err := c.Lookup(context.Background(), cd); err != nil {
		t.Fatal(err)
	}
}

func TestDecide(t *testing.T) {
	ctx := context.Background()
	local := Local{Artist: "Artiste", Album: "Le Disque", Year: "2015", Media: "Digital Media", Durations: []float64{200.4, 180.1, 240}}

	// Deux éditions aux mêmes pistes, même œuvre : certaine, le support du
	// tag MEDIA l'emporte.
	f := &fakeMB{releases: map[string]map[string]interface{}{
		cd:  edition(cd, rg, "Le Disque", "Artiste", "2015-03-01", "CD", 200000, 180000, 240500),
		web: edition(web, rg, "Le Disque", "Artiste", "2015-03-01", "Digital Media", 200000, 180000, 240500),
	}}
	f.server(t)
	res, err := client().Decide(ctx, local)
	if err != nil || !res.Certain || res.Best.ID != web {
		t.Fatalf("%v %+v", err, res)
	}

	// Les tags désignent l'édition : certaine sans recherche.
	tagged := local
	tagged.MBRelease = cd
	f.mu.Lock()
	f.queries = nil
	f.mu.Unlock()
	res, err = client().Decide(ctx, tagged)
	f.mu.Lock()
	nq := len(f.queries)
	f.mu.Unlock()
	if err != nil || !res.Certain || res.Best.ID != cd || !res.Best.FromTags || nq != 0 {
		t.Fatalf("%v %+v %v", err, res, f.queries)
	}

	// Les tags désignent une édition au nombre de pistes différent : à revoir.
	short := tagged
	short.Durations = local.Durations[:2]
	if res, _ = client().Decide(ctx, short); res.Certain || !strings.Contains(res.Reason, "3 pistes") {
		t.Fatalf("%+v", res)
	}

	// Durées différentes (autre version de l'album) : pas certaine.
	other := local
	other.Durations = []float64{260, 180, 240}
	if res, _ = client().Decide(ctx, other); res.Certain {
		t.Fatalf("durées différentes : %+v", res)
	}

	// Deux œuvres différentes aux mêmes pistes (album et réédition rangés à
	// part) : pas certaine.
	f.mu.Lock()
	f.releases[web]["release-group"] = map[string]interface{}{"id": oth, "primary-type": "Album"}
	f.mu.Unlock()
	if res, _ = client().Decide(ctx, local); res.Certain || !strings.Contains(res.Reason, "plusieurs albums") {
		t.Fatalf("deux œuvres : %+v", res)
	}

	// Rien trouvé.
	none := local
	none.Album = "Inexistant"
	if res, _ = client().Decide(ctx, none); res.Certain || res.Best != nil {
		t.Fatalf("rien : %+v", res)
	}
}

func TestSimilarity(t *testing.T) {
	for _, c := range []struct {
		a, b string
		min  float64
	}{
		{"Élégie pour l'été", "elegie pour lete", 1},
		{"Discovery", "Discovery", 1},
		{"The Dark Side of the Moon", "Dark Side Of The Moon", 0.8},
	} {
		if s := similarity(c.a, c.b); s < c.min {
			t.Errorf("%q / %q : %.2f", c.a, c.b, s)
		}
	}
	if similarity("Discovery", "Homework") != 0 {
		t.Error("titres sans rapport")
	}
}
