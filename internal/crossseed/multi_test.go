package crossseed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/links"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/qbit"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// seed : un torrent complet du faux client, rangé dans dir, annoncé à host.
func (f *fakeQbit) seed(raw []byte, dir, host string) string {
	p, _ := torrent.Parse(raw)
	h := strings.ToLower(p.InfoHash)
	f.list[h] = &fakeTorrent{Torrent: qbit.Torrent{
		Hash: h, Name: p.Name, SavePath: dir, ContentPath: dir + "/" + p.Name, Progress: 1, State: "stalledUP",
		Size: p.Total, TotalSize: p.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2F" + host + "%2Fannounce",
	}, raw: raw}
	return h
}

// fakeV3X : un faux V3X dont le catalogue est donné par empreinte de contenu
// (exact) ou de tailles (renamed).
type fakeRel struct {
	id   string
	kind string
	raw  []byte
}

func fakeV3X(t *testing.T, cat []fakeRel) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/d3x/match":
			var in struct{ Items []v3x.MatchItem }
			json.NewDecoder(r.Body).Decode(&in)
			var out []map[string]interface{}
			for _, it := range in.Items {
				res := map[string]interface{}{"key": it.Key, "match": nil}
				for _, c := range cat {
					p, _ := torrent.Parse(c.raw)
					if (c.kind == "exact" && it.ContentHash == p.ContentHash) || (c.kind == "renamed" && it.SizeHash == p.SizeHash) {
						res["match"] = v3x.Match{Kind: c.kind, ID: c.id, Name: p.Name, InfoHash: p.InfoHash, Seeders: 1, Size: p.Total}
					}
				}
				out = append(out, res)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"results": out})
		case "/torznab/download":
			for _, c := range cat {
				if c.id == r.URL.Query().Get("id") {
					w.Write(c.raw)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// Deux clients : la seedbox (premier des réglages) et le PC. Les listes sont
// fusionnées, chaque ligne porte son client, une release présente dans les
// deux n'a qu'une ligne (ajoutée au premier), ce qui est déjà dans l'un des
// clients n'est pas proposé, et chaque ajout part dans le client qui a les
// fichiers, avec son chemin et sa correspondance de dossiers.
func TestTwoClients(t *testing.T) {
	isolatedConfig(t)

	filesA := map[string]int64{"A.mkv": 3_000_000_000, "A.nfo": 4_000}
	filesS := map[string]int64{"S.mkv": 2_000_000_000}
	filesP := map[string]int64{"P.epub": 2_000_000}
	filesX := map[string]int64{"X.mkv": 1_000_000_000}
	other := func(name string, files map[string]int64) []byte {
		return mkTorrent(name, files, "https://autre.invalid/announce", "AUTRE")
	}
	theirs := func(name string, files map[string]int64) []byte {
		return mkTorrent(name, files, "https://announce.v3x.club/pk/announce", "")
	}
	v3xA, v3xS, v3xP, v3xX := theirs("Release.A", filesA), theirs("Release.S", filesS), theirs("Release.P", filesP), theirs("Release.X", filesX)
	// R : sur le PC sous d'autres noms ; sa racine V3X y est déjà l'emplacement
	// d'un autre torrent, d'où le repli par liens physiques.
	v3xR := theirs("Film.R", map[string]int64{"Film.R.mkv": 1_500_000_000})
	copyR := other("Ma Copie R", map[string]int64{"film r.mkv": 1_500_000_000})
	squat := other("Film.R", map[string]int64{"autre.mkv": 7})

	hashOf := func(raw []byte) string { p, _ := torrent.Parse(raw); return strings.ToLower(p.InfoHash) }
	box := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{hashOf(v3xA): 1, hashOf(v3xS): 1}}
	box.seed(other("Release.A", filesA), "/home/moi/data", "autre.invalid")
	box.seed(other("Release.S", filesS), "/home/moi/data", "autre.invalid")
	box.seed(other("Release.X", filesX), "/home/moi/data", "autre.invalid")
	pc := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}, disk: map[string]int64{
		"/pc/data/Release.P/P.epub": 2_000_000, "/pc/data/Ma Copie R/film r.mkv": 1_500_000_000, "/pc/data/Film.R/autre.mkv": 7,
	}}
	pc.seed(other("Release.A", filesA), "/pc/data", "troisieme.invalid") // mêmes fichiers que sur la seedbox
	pc.seed(other("Release.P", filesP), "/pc/data", "autre.invalid")
	pc.seed(v3xX, "/pc/v3x", "announce.v3x.club") // X : déjà dans le PC pour V3X
	pc.seed(copyR, "/pc/data", "autre.invalid")
	pc.seed(squat, "/pc/data", "autre.invalid")
	bs := httptest.NewServer(box.handler(t))
	defer bs.Close()
	ps := httptest.NewServer(pc.handler(t))
	defer ps.Close()
	cat := []fakeRel{{"aaaaaaaa-0000-4000-8000-000000000001", "exact", v3xA}, {"aaaaaaaa-0000-4000-8000-000000000002", "exact", v3xS}, {"aaaaaaaa-0000-4000-8000-000000000003", "exact", v3xP}, {"aaaaaaaa-0000-4000-8000-000000000004", "exact", v3xX}, {"aaaaaaaa-0000-4000-8000-000000000005", "renamed", v3xR}}
	vs := fakeV3X(t, cat)
	defer vs.Close()

	qb, _ := client.New(client.Options{URL: bs.URL, User: "admin", Pass: "secret"})
	qp, _ := client.New(client.Options{URL: ps.URL, User: "admin", Pass: "secret"})
	pcRules := []pathmap.Rule{{From: `P:\data`, To: "/pc/data"}}
	cfg := config.Defaults()
	cfg.Links = config.Links{On: true}
	cfg.Clients = []config.ClientConfig{{ID: "box", Name: "Seedbox", URL: bs.URL, LinksDir: "/home/moi/liens"}, {ID: "pc", Name: "PC", URL: ps.URL, PathMap: pcRules, LinksDir: "/pc/liens"}}
	env := &Env{V3X: v3x.New(vs.URL, "cle", "D3X/test"), Cfg: cfg, Targets: []Target{
		{ID: "box", Name: "Seedbox", Client: qb, LinksDir: "/home/moi/liens"},
		{ID: "pc", Name: "PC", Client: qp, PathMap: pcRules, LinksDir: "/pc/liens"},
	}}
	e := New(func() (*Env, error) { return env, nil })
	var (
		lmu  sync.Mutex
		made []string
	)
	e.SetLinker(func(_ context.Context, rules []pathmap.Rule, base string, pairs []links.Pair, remove bool) error {
		lmu.Lock()
		defer lmu.Unlock()
		if remove || base != "/pc/liens" || len(rules) != 1 || rules[0] != pcRules[0] {
			t.Errorf("liens : base %s, règles %v (attendu le dossier et les règles du PC)", base, rules)
			return nil
		}
		pc.mu.Lock()
		defer pc.mu.Unlock()
		for _, p := range pairs {
			pc.disk[p.Dst] = p.Size
			made = append(made, p.Src+" => "+p.Dst)
		}
		return nil
	})

	if err := e.StartScan(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !e.Scanning() })
	if st := e.State(); st.Scan.Phase != "done" || st.Scan.Warn != "" {
		t.Fatalf("analyse : %+v", st.Scan)
	}
	type got struct{ status, client string }
	rows := map[string][]got{}
	keys := map[string]string{}
	for _, r := range e.Rows() {
		rows[r.Name] = append(rows[r.Name], got{r.Status, r.ClientName})
		if r.Status == StAdd || (r.Status == StRenamed && r.CanAdd) {
			keys[r.Name] = r.Key
		}
		if r.Name == "Release.A" && r.Copies != 2 {
			t.Fatalf("Release.A : %d copies, 2 attendues (une par client)", r.Copies)
		}
	}
	want := map[string][]got{
		"Release.A":  {{StAdd, "Seedbox"}}, // présente dans les deux : une ligne, au premier client
		"Release.S":  {{StAdd, "Seedbox"}},
		"Release.P":  {{StAdd, "PC"}},
		"Release.X":  {{StInClient, "Seedbox"}, {StOnV3X, "PC"}}, // déjà dans le PC pour V3X
		"Ma Copie R": {{StRenamed, "PC"}},
		"Film.R":     {{StNone, "PC"}},
	}
	for name, w := range want {
		g := rows[name]
		if len(g) != len(w) {
			t.Fatalf("%s : %v, attendu %v", name, g, w)
		}
		for _, x := range w {
			found := false
			for _, y := range g {
				found = found || x == y
			}
			if !found {
				t.Fatalf("%s : %v, attendu %v", name, g, w)
			}
		}
	}

	var list []string
	for _, k := range keys {
		list = append(list, k)
	}
	if n, err := e.Apply(list); err != nil || n != 4 {
		t.Fatalf("Apply : %d %v (clés %v)", n, err, keys)
	}
	for _, raw := range [][]byte{v3xA, v3xS, v3xP, v3xR} {
		h := hashOf(raw)
		waitFor(t, func() bool { st, ok := e.WatchOf(h); return ok && st.Status == StSeeding })
	}

	box.mu.Lock()
	defer box.mu.Unlock()
	pc.mu.Lock()
	defer pc.mu.Unlock()
	savedIn := func(f *fakeQbit, raw []byte) string {
		if x := f.list[hashOf(raw)]; x != nil {
			return x.SavePath
		}
		return ""
	}
	// Chaque ajout dans le client qui a les fichiers, à leur emplacement.
	for _, c := range []struct {
		f    *fakeQbit
		raw  []byte
		want string
	}{{box, v3xA, "/home/moi/data"}, {box, v3xS, "/home/moi/data"}, {pc, v3xP, "/pc/data"}, {pc, v3xR, "/pc/liens"}} {
		if got := savedIn(c.f, c.raw); got != c.want {
			t.Fatalf("%s ajoutée dans %q, attendu %q", torrentName(c.raw), got, c.want)
		}
	}
	if savedIn(pc, v3xA) != "" || savedIn(pc, v3xS) != "" || savedIn(box, v3xP) != "" || savedIn(box, v3xR) != "" {
		t.Fatal("une release ajoutée dans le mauvais client")
	}
	if len(box.adds) != 2 || len(pc.adds) != 2 {
		t.Fatalf("ajouts : %d à la seedbox, %d au PC", len(box.adds), len(pc.adds))
	}
	lmu.Lock()
	defer lmu.Unlock()
	if len(made) != 1 || made[0] != "/pc/data/Ma Copie R/film r.mkv => /pc/liens/Film.R/Film.R.mkv" {
		t.Fatalf("liens : %v", made)
	}
}

func torrentName(raw []byte) string { p, _ := torrent.Parse(raw); return p.Name }

// Un client injoignable (déjà avant l'analyse, ou qui ne répond pas pendant)
// ne bloque pas les autres : l'analyse continue, le bandeau le dit, et un
// ajout pour lui attend qu'il revienne au lieu de partir ailleurs.
func TestClientDown(t *testing.T) {
	isolatedConfig(t)

	filesA := map[string]int64{"A.mkv": 3_000_000_000}
	v3xA := mkTorrent("Release.A", filesA, "https://announce.v3x.club/pk/announce", "")
	box := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}}
	box.seed(mkTorrent("Release.A", filesA, "https://autre.invalid/announce", "AUTRE"), "/data", "autre.invalid")
	bs := httptest.NewServer(box.handler(t))
	defer bs.Close()
	// Le PC : son interface web répond, mais plus sa liste.
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "x", Path: "/"})
			io.WriteString(w, "Ok.")
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ps.Close()
	vs := fakeV3X(t, []fakeRel{{"bbbbbbbb-0000-4000-8000-000000000001", "exact", v3xA}})
	defer vs.Close()

	qb, _ := client.New(client.Options{URL: bs.URL, User: "admin", Pass: "secret"})
	qp, _ := client.New(client.Options{URL: ps.URL, User: "admin", Pass: "secret"})
	cfg := config.Defaults()
	cfg.Clients = []config.ClientConfig{{ID: "nas", Name: "NAS", URL: "http://nas.invalid"}, {ID: "box", Name: "Seedbox", URL: bs.URL}, {ID: "pc", Name: "PC", URL: ps.URL}}
	var mu sync.Mutex
	env := &Env{V3X: v3x.New(vs.URL, "cle", "D3X/test"), Cfg: cfg,
		Targets: []Target{{ID: "box", Name: "Seedbox", Client: qb}, {ID: "pc", Name: "PC", Client: qp}},
		Down:    []Down{{ID: "nas", Name: "NAS", Error: "connexion refusée"}},
	}
	retried := 0
	e := New(func() (*Env, error) { mu.Lock(); defer mu.Unlock(); c := *env; return &c, nil })
	e.SetRefresh(func(context.Context) { mu.Lock(); retried++; mu.Unlock() })

	if err := e.StartScan(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !e.Scanning() })
	st := e.State()
	if st.Scan.Phase != "done" {
		t.Fatalf("analyse bloquée par un client injoignable : %+v", st.Scan)
	}
	if !strings.Contains(st.Scan.Warn, "NAS injoignable : connexion refusée") || !strings.Contains(st.Scan.Warn, "PC injoignable") {
		t.Fatalf("bandeau : %q", st.Scan.Warn)
	}
	mu.Lock()
	if retried != 1 {
		t.Fatalf("clients injoignables retentés %d fois au début de l'analyse, 1 attendue", retried)
	}
	mu.Unlock()
	rows := e.Rows()
	if len(rows) != 1 || rows[0].Status != StAdd || rows[0].ClientName != "Seedbox" {
		t.Fatalf("lignes : %+v", rows)
	}

	// La seedbox tombe à son tour avant l'ajout : rien ne part ailleurs, la
	// ligne reste à ajouter avec la raison.
	mu.Lock()
	env.Targets = env.Targets[1:]
	env.Down = append(env.Down, Down{ID: "box", Name: "Seedbox", Error: "délai dépassé"})
	mu.Unlock()
	if n, err := e.Apply([]string{rows[0].Key}); err != nil || n != 1 {
		t.Fatalf("Apply : %d %v", n, err)
	}
	waitFor(t, func() bool { st := e.State(); return !st.Applying })
	r := e.Rows()[0]
	if r.Status != StAdd || r.Detail != "Seedbox injoignable : délai dépassé" {
		t.Fatalf("après ajout vers un client injoignable : %s %q", r.Status, r.Detail)
	}

	// Tous injoignables (le PC ne rend toujours pas sa liste) : l'analyse
	// échoue en le disant.
	if err := e.StartScan(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !e.Scanning() })
	if st := e.State(); st.Scan.Phase != "error" || !strings.Contains(st.Scan.Error, "PC injoignable") {
		t.Fatalf("tous injoignables : %+v", st.Scan)
	}
}
