package crossseed

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/qbit"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// Copie que le client croit complète mais dont le fichier a disparu du disque
// (V3x0r, 21/10/26 : supprimée ou déplacée par un outil de médiathèque). D3X
// ne l'ajoute pas et le dit, au lieu d'une vérification à 0 % « fichiers
// différents ».
func TestMissingCopy(t *testing.T) {
	isolatedConfig(t)
	files := map[string]int64{"M.mkv": 2_000_000_000}
	other := mkTorrent("Release.M", files, "https://autre.invalid/announce", "AUTRE")
	onV3X := mkTorrent("Release.M", files, "https://announce.v3x.club/pk/announce", "")
	po, _ := torrent.Parse(other)
	pv, _ := torrent.Parse(onV3X)
	h := strings.ToLower(po.InfoHash)

	fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{},
		// Le disque garde un autre fichier du dossier, pas celui de la copie.
		disk: map[string]int64{"/data/autre/Autre.mkv": 1}}
	fq.list[h] = &fakeTorrent{Torrent: qbit.Torrent{
		Hash: h, Name: po.Name, SavePath: "/data/autre", ContentPath: "/data/autre/" + po.Name, Progress: 1, State: "stalledUP",
		Size: po.Total, TotalSize: po.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2Fautre.invalid%2Fannounce",
	}, raw: other}
	qs := httptest.NewServer(fq.handler(t))
	defer qs.Close()

	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
		case "/api/d3x/match":
			var in struct{ Items []v3x.MatchItem }
			json.NewDecoder(r.Body).Decode(&in)
			type res struct {
				Key   string     `json:"key"`
				Match *v3x.Match `json:"match"`
			}
			var out []res
			for _, it := range in.Items {
				out = append(out, res{Key: it.Key, Match: &v3x.Match{Kind: "exact", ID: "33333333-3333-3333-3333-333333333333", Name: "Release.M", InfoHash: pv.InfoHash, Seeders: 3, Size: pv.Total}})
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"results": out})
		case "/torznab/download":
			w.Write(onV3X)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()

	q, _ := client.New(client.Options{URL: qs.URL, User: "admin", Pass: "secret"})
	e := New(oneClient(q, v3x.New(vs.URL, "cle", "D3X/test"), config.Defaults()))
	if err := e.StartScan(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !e.Scanning() })
	var key string
	for _, r := range e.Rows() {
		if r.Name == "Release.M" && r.Status == StAdd {
			key = r.Key
		}
	}
	if key == "" {
		t.Fatalf("Release.M devait être à ajouter : %+v", e.Rows())
	}
	if _, err := e.Apply([]string{key}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		for _, r := range e.Rows() {
			if r.Key == key {
				return r.Status != StQueued && r.Status != StAdd
			}
		}
		return false
	})
	for _, r := range e.Rows() {
		if r.Key == key && (r.Status != StMissing || r.Detail != missingDetail) {
			t.Fatalf("copie absente : %s (%s), attendu %s", r.Status, r.Detail, StMissing)
		}
	}
	fq.mu.Lock()
	n := len(fq.list)
	fq.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d torrents dans le client : rien ne devait être ajouté", n)
	}
}
