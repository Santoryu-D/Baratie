package crossseed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/links"
	"github.com/V3x0rV3x/D3X/internal/maketorrent"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// testLib : la médiathèque sur le disque du test ; le client la voit sous
// « /lib ».
type testLib struct{ root string }

func (l testLib) Files(_ context.Context, _, dir string) ([]LibFile, error) {
	var out []LibFile
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			out = append(out, LibFile{Path: p, Size: fi.Size()})
		}
		return err
	})
	return out, err
}

func (l testLib) ReadAt(_ context.Context, _, p string, off, n int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	k, _ := f.ReadAt(b, off)
	return b[:k], nil
}

func (l testLib) ClientPath(_, p string, _ []pathmap.Rule) string {
	rel, _ := filepath.Rel(l.root, p)
	return "/lib/" + filepath.ToSlash(rel)
}

func fill(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31) ^ seed ^ byte(i>>9)
	}
	return b
}

// hashed : le .torrent V3X d'un dossier fait exprès (mêmes données, autres noms).
func hashed(t *testing.T, dir string) []byte {
	p, err := maketorrent.Scan(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := p.Hash(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLibrary(t *testing.T) {
	isolatedConfig(t)
	old := libMinSize
	libMinSize = 100_000
	t.Cleanup(func() { libMinSize = old })

	work := t.TempDir()
	lib := filepath.Join(work, "medias")
	film := fill(3_000_000, 1)
	write := func(p string, b []byte) {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// La médiathèque : le film renommé, un autre fichier.
	write(filepath.Join(lib, "Film (2020)", "Film (2020).mkv"), film)
	write(filepath.Join(lib, "Autre (2019)", "Autre (2019).mkv"), fill(2_000_000, 2))
	// Les releases V3X, fabriquées ailleurs.
	rel := filepath.Join(work, "v3x")
	write(filepath.Join(rel, "Film.2020.1080p.WEB-GRP", "Film.2020.1080p.WEB-GRP.mkv"), film)
	rawFilm := hashed(t, filepath.Join(rel, "Film.2020.1080p.WEB-GRP"))
	write(filepath.Join(rel, "Autre.2019.1080p.WEB-GRP.mkv"), fill(2_000_000, 9)) // même taille, autres données
	rawAutre := hashed(t, filepath.Join(rel, "Autre.2019.1080p.WEB-GRP.mkv"))
	write(filepath.Join(rel, "Film.2020.MULTi.1080p-GRP", "Film.2020.MULTi.1080p-GRP.mkv"), film)
	write(filepath.Join(rel, "Film.2020.MULTi.1080p-GRP", "Film.2020.MULTi.1080p-GRP.nfo"), []byte("nfo"))
	rawNfo := hashed(t, filepath.Join(rel, "Film.2020.MULTi.1080p-GRP")) // un .nfo absent de la médiathèque

	type rel3 struct {
		id  string
		raw []byte
	}
	cat := []rel3{{"11111111-1111-4111-8111-111111111111", rawFilm}, {"22222222-2222-4222-8222-222222222222", rawAutre}, {"33333333-3333-4333-8333-333333333333", rawNfo}}

	fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}, disk: map[string]int64{
		"/lib/Film (2020)/Film (2020).mkv": 3_000_000, "/lib/Autre (2019)/Autre (2019).mkv": 2_000_000,
	}}
	qs := httptest.NewServer(fq.handler(t))
	defer qs.Close()
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
		case "/api/d3x/match-files":
			var in struct{ Sizes []int64 }
			json.NewDecoder(r.Body).Decode(&in)
			out := map[string][]v3x.FileMatch{}
			for _, s := range in.Sizes {
				if s < libMinSize {
					t.Errorf("taille envoyée sous le seuil : %d", s)
				}
				for _, c := range cat {
					p, _ := torrent.Parse(c.raw)
					for _, f := range p.Files {
						if f.Size == s {
							k := strconv.FormatInt(s, 10)
							out[k] = append(out[k], v3x.FileMatch{ID: c.id, Name: p.Name, InfoHash: p.InfoHash, Size: p.Total, FileCount: len(p.Files), Seeders: 0})
						}
					}
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"matches": out})
		case "/torznab/download":
			for _, c := range cat {
				if c.id == r.URL.Query().Get("id") {
					w.Write(c.raw)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case "/api/d3x/match":
			io.WriteString(w, `{"results":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()

	q, _ := client.New(client.Options{URL: qs.URL, User: "admin", Pass: "secret"})
	v := v3x.New(vs.URL, "cle", "D3X/test")
	cfg := config.Defaults()
	e := New(oneClient(q, v, cfg))
	e.SetLibraryIO(testLib{root: lib})
	if err := e.StartLibrary([]config.LibFolder{{Path: lib, Source: "local"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { st := e.Library(); return st.Scan.Phase == "done" || st.Scan.Phase == "error" })
	if st := e.Library(); st.Scan.Phase != "done" {
		t.Fatalf("analyse : %+v", st.Scan)
	}
	got := map[string]LibRow{}
	for _, r := range e.LibraryRows() {
		got[r.Match.ID] = r
	}
	film1 := got[cat[0].id]
	if film1.Status != StAdd || !strings.Contains(film1.Detail, "identique") {
		t.Fatalf("film : %s %s", film1.Status, film1.Detail)
	}
	if r := got[cat[1].id]; r.Status != StLibDifferent {
		t.Fatalf("autre : %s %s", r.Status, r.Detail)
	}
	// Il ne manque que le .nfo : à compléter (MiTor, 23/10/26).
	if r := got[cat[2].id]; r.Status != StLibComplete || len(r.Missing) != 1 || !strings.HasSuffix(r.Missing[0], ".nfo") || r.MissBytes <= 0 {
		t.Fatalf("avec nfo : %s %v %d", r.Status, r.Missing, r.MissBytes)
	}

	// Ajout : renommage dans le client, sur le fichier de la médiathèque.
	if n, err := e.ApplyLibrary([]string{film1.Key, got[cat[1].id].Key}); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	pf, _ := torrent.Parse(rawFilm)
	h := strings.ToLower(pf.InfoHash)
	waitFor(t, func() bool { st, ok := e.WatchOf(h); return ok && st.Status == StSeeding })
	fq.mu.Lock()
	defer fq.mu.Unlock()
	if fq.list[h].SavePath != "/lib/Film (2020)" {
		t.Fatalf("ajouté dans %s", fq.list[h].SavePath)
	}
	names := fq.names[h]
	if len(names) != 1 || names[0] != "Film (2020).mkv" {
		t.Fatalf("noms dans le client : %v", names)
	}
	// Seule la release vérifiée a été ajoutée.
	if len(fq.list) != 1 {
		t.Fatalf("%d torrents dans le client", len(fq.list))
	}
	fq.mu.Unlock()
	// Sans dossier de liens, compléter est refusé : la ligne reste à compléter.
	if n, err := e.ApplyLibrary([]string{got[cat[2].id].Key}); err != nil || n != 1 {
		t.Fatalf("compléter : %d %v", n, err)
	}
	waitFor(t, func() bool {
		for _, r := range e.LibraryRows() {
			if r.Key == got[cat[2].id].Key {
				return r.Status == StLibComplete && strings.Contains(r.Detail, "dossier de liens")
			}
		}
		return false
	})
	fq.mu.Lock()
}

// Compléter puis seeder (MiTor, 23/10/26) : dans le dossier de liens, le
// client vérifie ce qui est là, puis démarre pour télécharger le .nfo manquant.
func TestLibraryComplete(t *testing.T) {
	isolatedConfig(t)
	old := libMinSize
	libMinSize = 100_000
	t.Cleanup(func() { libMinSize = old })
	work := t.TempDir()
	lib := filepath.Join(work, "medias")
	film := fill(3_000_000, 1)
	write := func(p string, b []byte) {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(lib, "Film (2020)", "Film (2020).mkv"), film)
	rel := filepath.Join(work, "v3x", "Film.2020.MULTi.1080p-GRP")
	write(filepath.Join(rel, "Film.2020.MULTi.1080p-GRP.mkv"), film)
	write(filepath.Join(rel, "Film.2020.MULTi.1080p-GRP.nfo"), []byte("nfo de la release"))
	raw := hashed(t, rel)
	p, _ := torrent.Parse(raw)
	const id = "33333333-3333-4333-8333-333333333333"
	fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}, disk: map[string]int64{}}
	qs := httptest.NewServer(fq.handler(t))
	defer qs.Close()
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
		case "/api/d3x/match-files":
			out := map[string][]v3x.FileMatch{"3000000": {{ID: id, Name: p.Name, InfoHash: p.InfoHash, Size: p.Total, FileCount: 2}}}
			json.NewEncoder(w).Encode(map[string]interface{}{"matches": out})
		case "/torznab/download":
			w.Write(raw)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()
	q, _ := client.New(client.Options{URL: qs.URL, User: "admin", Pass: "secret"})
	cfg := config.Defaults()
	cfg.Links = config.Links{On: true, Dir: "/data/liens"}
	e := New(oneClient(q, v3x.New(vs.URL, "cle", "D3X/test"), cfg))
	e.SetLibraryIO(testLib{root: lib})
	var made []links.Pair
	e.SetLinker(func(_ context.Context, _ []pathmap.Rule, base string, pairs []links.Pair, remove bool) error {
		if remove {
			return nil
		}
		fq.mu.Lock()
		defer fq.mu.Unlock()
		for _, x := range pairs {
			fq.disk[x.Dst] = x.Size
		}
		made = append(made, pairs...)
		return nil
	})
	if err := e.StartLibrary([]config.LibFolder{{Path: lib, Source: "local"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { st := e.Library(); return st.Scan.Phase == "done" || st.Scan.Phase == "error" })
	var row LibRow
	for _, r := range e.LibraryRows() {
		row = r
	}
	if row.Status != StLibComplete {
		t.Fatalf("%s %s", row.Status, row.Detail)
	}
	if n, err := e.ApplyLibrary([]string{row.Key}); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	h := strings.ToLower(p.InfoHash)
	waitFor(t, func() bool { st, ok := e.WatchOf(h); return ok && st.Status == StSeeding })
	if st, _ := e.WatchOf(h); !strings.Contains(st.Detail, "petits fichiers manquants") {
		t.Fatalf("détail : %s", st.Detail)
	}
	// Seule la vidéo a un lien : le .nfo, absent, sera téléchargé par le client.
	if len(made) != 1 || !strings.HasSuffix(made[0].Dst, ".mkv") {
		t.Fatalf("liens : %+v", made)
	}
}
