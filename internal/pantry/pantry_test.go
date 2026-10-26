package pantry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"baratie/internal/config"
	"baratie/internal/tmdb"
	"baratie/internal/tracker"
	"baratie/internal/unit3d"
	"baratie/internal/unit3d/fake"
)

const big = 300 << 20

// mk : un fichier de cette taille (creux : aucune place prise).
func mk(t *testing.T, p string, size int64) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(size)
	f.Close()
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	cin := filepath.Join(root, "Cinéma")
	mk(t, filepath.Join(cin, "Inception (2010)", "Inception (2010) Bluray-1080p.mkv"), big)
	mk(t, filepath.Join(cin, "Inception (2010)", "Sample", "sample.mkv"), big)
	mk(t, filepath.Join(cin, "Inception (2010)", "Inception-trailer.mkv"), big)
	mk(t, filepath.Join(cin, "Inception (2010)", "poster.jpg"), 1000)
	mk(t, filepath.Join(cin, "Extras", "Making of.mkv"), big)
	mk(t, filepath.Join(cin, "Petit.mkv"), 10<<20)
	mk(t, filepath.Join(cin, ".cache", "x.mkv"), big)
	mk(t, filepath.Join(cin, "Inception (2010)", "Plex Versions", "Optimized for TV", "Inception (2010).mp4"), big)
	mk(t, filepath.Join(cin, "Film.Seul.1999.FRENCH.1080p.BluRay.x264-GRP.mkv"), big+1)
	found, errs := Scan([]string{cin, filepath.Join(root, "absente")})
	if len(found) != 2 || len(errs) != 1 {
		t.Fatalf("%+v %v", found, errs)
	}
	if found[0].Lib != "Cinéma" || !strings.HasSuffix(found[0].Rel, ".mkv") {
		t.Fatalf("%+v", found[0])
	}
	parse := func(s string) (string, string) {
		if strings.Contains(s, "2010") {
			return "Inception", "2010"
		}
		return s, ""
	}
	f := Found{Path: filepath.Join(cin, "Inception (2010)", "film.mkv"), Lib: "Cinéma"}
	if NameFor(f, parse) != "Inception (2010)" {
		t.Fatalf("nom : %q", NameFor(f, parse))
	}
}

type world struct {
	root, cin, nanar string
	trk              *fake.Server
	sizeIdentique    int64
}

func setup(t *testing.T) (*Manager, *world) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("AppData", tmp)
	w := &world{root: tmp, cin: filepath.Join(tmp, "media", "Cinéma"), nanar: filepath.Join(tmp, "media", "nanar"), sizeIdentique: big + 777}
	mk(t, filepath.Join(w.cin, "Inception (2010)", "Inception (2010) Bluray-1080p.mkv"), big)
	mk(t, filepath.Join(w.cin, "Le.Film.Rare.1999.FRENCH.1080p.BluRay.x264-GRP.mkv"), big+1)
	mk(t, filepath.Join(w.cin, "Film.Couvert.2005.MULTi.1080p.AMZN.WEB-DL.H264-X.mkv"), big+2)
	mk(t, filepath.Join(w.cin, "Film.Autre.Source.2007.MULTi.1080p.WEB.H264-X.mkv"), big+8)
	mk(t, filepath.Join(w.cin, "Film.Peut.Etre.2008.MULTi.1080p.WEB.H264-X.mkv"), big+9)
	mk(t, filepath.Join(w.cin, "Film.Complement.2006.MULTi.1080p.WEB.H264-X.mkv"), big+3)
	mk(t, filepath.Join(w.cin, "Mon film perso.mkv"), big+4)
	mk(t, filepath.Join(w.cin, "Bad.Film.2020.MULTi.1080p.WEB.H264-BADTEAM.mkv"), big+5)
	mk(t, filepath.Join(w.cin, "Identique.2015.MULTi.1080p.WEB.H264-Y.mkv"), w.sizeIdentique)
	mk(t, filepath.Join(w.nanar, "Nanar.1985.FRENCH.720p.HDTV.x264-Z.mkv"), big+6)

	// TMDB : un film par titre ; « Mon film perso » est ambigu.
	ids := map[string][]map[string]interface{}{
		"le film rare":      {{"id": 101, "title": "Le Film Rare", "release_date": "1999-01-01"}},
		"film couvert":      {{"id": 102, "title": "Film Couvert", "release_date": "2005-01-01"}},
		"film autre source": {{"id": 107, "title": "Film Autre Source", "release_date": "2007-01-01"}},
		"film peut etre":    {{"id": 108, "title": "Film Peut Etre", "release_date": "2008-01-01"}},
		"film complement":   {{"id": 103, "title": "Film Complement", "release_date": "2006-01-01"}},
		"mon film perso":    {{"id": 201, "title": "Mon film", "release_date": "2001-01-01"}, {"id": 202, "title": "Perso", "release_date": "2003-01-01"}},
		"bad film":          {{"id": 104, "title": "Bad Film", "release_date": "2020-01-01"}},
		"identique":         {{"id": 105, "title": "Identique", "release_date": "2015-01-01"}},
		"nanar":             {{"id": 106, "title": "Nanar", "release_date": "1985-01-01"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		res := ids[strings.ToLower(r.URL.Query().Get("query"))]
		json.NewEncoder(rw).Encode(map[string]interface{}{"results": res})
	}))
	t.Cleanup(srv.Close)
	old := tmdb.Base
	tmdb.Base = srv.URL
	t.Cleanup(func() { tmdb.Base = old })

	// Radarr : connaît Inception par son dossier.
	rad := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "cle" {
			rw.WriteHeader(401)
			return
		}
		json.NewEncoder(rw).Encode([]map[string]interface{}{{"tmdbId": 27205, "path": filepath.Join(w.cin, "Inception (2010)")}})
	}))
	t.Cleanup(rad.Close)

	w.trk = fake.New(
		&fake.Torrent{ID: 1, Name: "Inception.2010.MULTi.1080p.WEB.H265-CHiLL", Category: 1, Resolution: "1080p", Type: "WEB", TMDB: 27205, Files: []int64{3 << 30}},
		&fake.Torrent{ID: 2, Name: "Film.Couvert.2005.FRENCH.1080p.AMZN.WEB-DL.DDP5.1.H264-AUTRE", Category: 1, Resolution: "1080p", Type: "WEB", TMDB: 102, Files: []int64{2 << 30}},
		&fake.Torrent{ID: 5, Name: "Film.Autre.Source.2007.FRENCH.1080p.BluRay.x264-AUTRE", Category: 1, Resolution: "1080p", Type: "Encode", TMDB: 107, Files: []int64{2 << 30}},
		&fake.Torrent{ID: 6, Name: "Film.Peut.Etre.2008.MULTi.1080p.NF.WEB-DL.H264-AUTRE", Category: 1, Resolution: "1080p", Type: "WEB", TMDB: 108, Files: []int64{2 << 30}},
		&fake.Torrent{ID: 3, Name: "Film.Complement.2006.VOSTFR.720p.WEB.H264-AUTRE", Category: 6, Resolution: "720p", Type: "WEB", TMDB: 103, Files: []int64{1 << 30}},
		&fake.Torrent{ID: 4, Name: "Identique.2015.MULTi.1080p.WEB.H264-Y", Category: 1, Resolution: "1080p", Type: "WEB", TMDB: 105, Files: []int64{w.sizeIdentique}},
	)
	t.Cleanup(w.trk.Close)
	trk := unit3d.New(w.trk.URL, fake.Token, "test", fake.Rules())
	cfg := config.Config{PantryLibs: []string{w.cin, w.nanar}, RadarrURL: rad.URL, RadarrKey: "cle"}
	m := New(Deps{
		Cfg:     func() config.Config { return cfg },
		Tracker: func() tracker.Tracker { return trk },
		TMDB:    func() *tmdb.Client { return tmdb.New("cle") },
	})
	m.interval = 5 * time.Millisecond
	m.tmdbGap = 0
	return m, w
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("délai dépassé")
}

func classes(m *Manager) map[string]string {
	out := map[string]string{}
	for _, it := range m.Items() {
		out[filepath.Base(it.Path)] = it.Class
	}
	return out
}

func TestPantryFlow(t *testing.T) {
	m, w := setup(t)
	if err := m.Scan(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s := m.State(); return s.Phase == "idle" && s.Queue == 0 && s.Checking == "" })
	got := classes(m)
	want := map[string]string{
		"Inception (2010) Bluray-1080p.mkv":                    ClassEvaluate,  // Radarr ; langue absente du nom
		"Le.Film.Rare.1999.FRENCH.1080p.BluRay.x264-GRP.mkv":   ClassAbsent,    // le tracker n'a rien
		"Film.Couvert.2005.MULTi.1080p.AMZN.WEB-DL.H264-X.mkv": ClassCovered,   // même plateforme, résolution, codec
		"Film.Autre.Source.2007.MULTi.1080p.WEB.H264-X.mkv":    ClassComplete,  // le tracker n'a qu'un encode Blu-ray
		"Film.Peut.Etre.2008.MULTi.1080p.WEB.H264-X.mkv":       ClassMaybe,     // plateforme absente du nom
		"Film.Complement.2006.MULTi.1080p.WEB.H264-X.mkv":      ClassComplete,  // seulement du 720p VOSTFR
		"Mon film perso.mkv":                                   ClassUnknown,   // ambigu : à toi de choisir
		"Bad.Film.2020.MULTi.1080p.WEB.H264-BADTEAM.mkv":       ClassExcluded,  // team interdite
		"Identique.2015.MULTi.1080p.WEB.H264-Y.mkv":            ClassIdentical, // mêmes octets
		"Nanar.1985.FRENCH.720p.HDTV.x264-Z.mkv":               ClassAbsent,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s : %s, attendu %s", k, got[k], v)
		}
	}
	st := m.State()
	if !strings.Contains(st.Radarr, "1 film reconnu") {
		t.Fatalf("Radarr : %q", st.Radarr)
	}
	// La team interdite n'est jamais cherchée sur le tracker ; les autres une fois chacune.
	if w.trk.Filters != 8 {
		t.Fatalf("%d recherches sur le tracker", w.trk.Filters)
	}
	// Un film ambigu, choisi à la main : cherché puis classé.
	var perso Item
	for _, it := range m.Items() {
		if filepath.Base(it.Path) == "Mon film perso.mkv" {
			perso = it
		}
	}
	if len(perso.Candidates) != 2 {
		t.Fatalf("candidats : %+v", perso.Candidates)
	}
	m.SetMovie(perso.ID, perso.Candidates[0])
	waitFor(t, func() bool { it, _ := m.Get(perso.ID); return it.Class != ClassPending && !it.CheckedAt.IsZero() })
	if it, _ := m.Get(perso.ID); it.Class != ClassAbsent || it.Source != SrcManual {
		t.Fatalf("après choix : %s %s", it.Class, it.Source)
	}
	// Le cache survit au redémarrage, sans nouvelle recherche.
	m.Flush()
	before := w.trk.Filters
	m2 := New(Deps{Cfg: m.deps.Cfg, Tracker: m.deps.Tracker, TMDB: m.deps.TMDB})
	m2.interval = 5 * time.Millisecond
	if c := classes(m2); c["Le.Film.Rare.1999.FRENCH.1080p.BluRay.x264-GRP.mkv"] != ClassAbsent {
		t.Fatalf("cache : %v", c)
	}
	time.Sleep(100 * time.Millisecond)
	if w.trk.Filters != before {
		t.Fatal("recherches refaites alors que le cache est frais")
	}
}

// Le scan ne touche à rien : mêmes fichiers, mêmes dates, rien de créé.
func TestPantryReadOnly(t *testing.T) {
	m, w := setup(t)
	snap := func() map[string]string {
		out := map[string]string{}
		filepath.Walk(filepath.Join(w.root, "media"), func(p string, fi os.FileInfo, err error) error {
			if err == nil {
				out[p] = fi.ModTime().String() + "|" + itoa(int(fi.Size()))
			}
			return nil
		})
		return out
	}
	before := snap()
	m.Scan()
	waitFor(t, func() bool { s := m.State(); return s.Phase == "idle" && s.Queue == 0 })
	after := snap()
	if len(before) != len(after) {
		t.Fatalf("fichiers : %d avant, %d après", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s modifié", k)
		}
	}
}

// En pause : aucune recherche sur le tracker.
func TestPantryPause(t *testing.T) {
	m, w := setup(t)
	m.Pause(true)
	m.Scan()
	waitFor(t, func() bool { return m.State().Phase == "idle" })
	time.Sleep(150 * time.Millisecond)
	if w.trk.Filters != 0 {
		t.Fatalf("%d recherches malgré la pause", w.trk.Filters)
	}
	m.Pause(false)
	waitFor(t, func() bool { return m.State().Queue == 0 })
}

// Une bibliothèque décochée : ses films sont cachés et plus cherchés, mais
// gardés en mémoire ; recochée, ils reviennent sans nouvelle recherche.
func TestPantryLibsToggle(t *testing.T) {
	m, w := setup(t)
	m.Scan()
	waitFor(t, func() bool { s := m.State(); return s.Phase == "idle" && s.Queue == 0 && s.Checking == "" })
	all := len(m.Items())
	cfg := m.deps.Cfg()
	only := cfg
	only.PantryLibs = []string{w.nanar}
	m.deps.Cfg = func() config.Config { return only }
	m.Scan()
	waitFor(t, func() bool { return m.State().Phase == "idle" })
	if n := len(m.Items()); n != 1 {
		t.Fatalf("%d films avec nanar seul", n)
	}
	before := w.trk.Filters
	m.deps.Cfg = func() config.Config { return cfg }
	m.Scan()
	waitFor(t, func() bool { s := m.State(); return s.Phase == "idle" && s.Queue == 0 })
	if n := len(m.Items()); n != all || w.trk.Filters != before {
		t.Fatalf("%d films (attendu %d), %d recherches de plus", n, all, w.trk.Filters-before)
	}
}

func TestClassifyHDLight(t *testing.T) {
	it := &Item{Found: Found{Path: "/data/media/Cinéma/Film (2020)/Film.2020.FRENCH.1080p.HDLight.x264-GRP.mkv"}, Name: "Film (2020)", TMDB: 1}
	if c, why := Classify(it, fake.Rules().Fiche()); c != ClassExcluded || !strings.Contains(why, "HDLIGHT") {
		t.Fatalf("%s %s", c, why)
	}
}
