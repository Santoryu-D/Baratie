package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/bencode"
	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/crossseed"
	"github.com/V3x0rV3x/D3X/internal/source"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// Release déjà sur V3X à l'identique (Turias, 22/10/26 : Publier renvoyait
// vers Cross-seed, qui ne la voyait pas) : rien n'est publié, le .torrent de la
// fiche V3X est ajouté à l'arrêt sur ces fichiers, vérifié, puis démarré.
func TestSeedExisting(t *testing.T) {
	parent, dir := release(t)
	ctx := context.Background()
	ex := config.Defaults().ExcludeList()
	plan, err := source.Local{}.Scan(ctx, dir, ex)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := source.Local{}.Hash(ctx, plan, ex, "V3X", func(int64, int64) {})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := bencode.Decode(raw)
	root := d.Root.(bencode.Dict)
	root["announce"] = []byte("https://announce.v3x.club/pk/announce")
	onV3X, _ := bencode.Encode(root)
	p, _ := torrent.Parse(onV3X)
	const id = "bbbbbbbb-0000-4000-8000-000000000002"

	var uploads atomic.Int32
	site := &fakeSite{}
	site.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/d3x/match":
			json.NewEncoder(w).Encode(map[string]interface{}{"results": []map[string]interface{}{{"key": "0", "match": v3x.Match{Kind: "exact", ID: id, Name: p.Name, InfoHash: p.InfoHash, Seeders: 3}}}})
		case "/torznab/download":
			if r.URL.Query().Get("id") != id {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(onV3X)
		case "/api/torrents":
			uploads.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(site.srv.Close)

	fc := newFakeClient(t)
	q, _ := client.New(client.Options{URL: fc.srv.URL})
	clients := envOf(site, crossseed.Target{ID: "c1", Name: "qBittorrent", Client: q})
	m := New(clients, crossseed.New(clients), "test")

	j, err := m.Start(source.Local{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if j.Duplicate == nil || j.Duplicate.Kind != "exact" {
		t.Fatalf("doublon attendu : %+v", j.Duplicate)
	}
	if err := m.SeedExisting(j.ID); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		g, _ := m.Get(j.ID)
		return g.State == StDone && g.Seed != nil && g.Seed.Status == crossseed.StSeeding
	})
	g, _ := m.Get(j.ID)
	if !g.SeedOnly || g.SeedError != "" || g.Published != nil || uploads.Load() != 0 {
		t.Fatalf("seed seul attendu : %+v (envois au site : %d)", g, uploads.Load())
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.added["savepath"] != parent || fc.added["stopped"] != "true" {
		t.Fatalf("ajout au client : %v", fc.added)
	}
	if len(fc.started) != 1 || !strings.EqualFold(fc.started[0], p.InfoHash) {
		t.Fatalf("démarrés : %v, attendu %s", fc.started, p.InfoHash)
	}
	// Une seconde fois : déjà en route.
	if err := m.SeedExisting(j.ID); err == nil {
		t.Fatal("deuxième demande acceptée")
	}
}
