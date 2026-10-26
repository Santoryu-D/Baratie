package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"baratie/internal/bencode"
	"baratie/internal/client"
	"baratie/internal/config"
	"baratie/internal/maketorrent"
	"baratie/internal/seed"
	"baratie/internal/torrent"
	"baratie/internal/tracker"
)

// fakeTracker : un tracker UNIT3D en mémoire. Comme le vrai, il réécrit le
// .torrent reçu (source, entropy) : l'empreinte change.
type fakeTracker struct {
	mu       sync.Mutex
	existing []tracker.Version
	fields   tracker.Fields
	nfo      []byte
	site     []byte
	uploads  int
}

func (f *fakeTracker) ID() string                  { return "fake" }
func (f *fakeTracker) Name() string                { return "Faux" }
func (f *fakeTracker) TorrentURL(id string) string { return "https://faux.example/torrents/" + id }
func (f *fakeTracker) Context() *tracker.PubContext {
	return &tracker.PubContext{Tracker: "fake", Name: "Faux"}
}
func (f *fakeTracker) Check(ctx context.Context) (*tracker.Account, error) {
	return &tracker.Account{Username: "membre"}, nil
}
func (f *fakeTracker) Existing(ctx context.Context, q tracker.ExistingQuery) ([]tracker.Version, error) {
	return append([]tracker.Version(nil), f.existing...), nil
}
func (f *fakeTracker) Upload(ctx context.Context, name string, raw, nfo []byte, fl tracker.Fields) (*tracker.Uploaded, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads++
	f.fields, f.nfo = fl, nfo
	d, err := bencode.Decode(raw)
	if err != nil {
		return nil, err
	}
	root := d.Root.(bencode.Dict)
	info := root["info"].(bencode.Dict)
	info["source"] = []byte("FAUX")
	info["entropy"] = []byte("0123456789abcdef")
	delete(root, "announce")
	f.site, _ = bencode.Encode(root)
	return &tracker.Uploaded{ID: "42", Status: "approved", Download: "https://faux.example/torrent/download/42.rsskey"}, nil
}
func (f *fakeTracker) SeedTorrent(ctx context.Context, up *tracker.Uploaded) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.site, nil
}

func trackerEnv(trk tracker.Tracker, targets ...seed.Target) seed.Clients {
	cfg := config.Defaults()
	for _, t := range targets {
		cfg.Clients = append(cfg.Clients, config.ClientConfig{ID: t.ID, Name: t.Name, PathMap: t.PathMap})
	}
	return func() (*seed.Env, error) {
		c := *cfg
		return &seed.Env{Tracker: trk, Cfg: &c, Targets: targets}, nil
	}
}

// Parcours complet : publication vers le tracker, puis seed avec le
// .torrent réécrit par le site (pas le .torrent local).
func TestPublishToTracker(t *testing.T) {
	parent, dir := release(t)
	fc := newFakeClient(t)
	trk := &fakeTracker{}
	q, _ := client.New(client.Options{URL: fc.srv.URL})
	clients := trackerEnv(trk, seed.Target{ID: "c1", Name: "qBittorrent", Client: q})
	m := New(clients, seed.New(clients), "test")

	j, err := m.Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	if j.Tracker != "fake" {
		t.Fatalf("départ : %+v", j)
	}
	wait(t, func() bool { g, _ := m.Get(j.ID); return g.State == StReady })
	g, _ := m.Get(j.ID)
	local := g.InfoHash

	f := tracker.Fields{Name: "Film.2024.1080p.WEB.H264-GRP", Description: "Film.2024.1080p.WEB.H264-GRP", CategoryID: 1, TypeID: 4, ResolutionID: 3, TMDB: 27205, IMDB: 1375666}
	if err := m.PublishTo(j.ID, f, []byte("notes")); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		g, _ := m.Get(j.ID)
		return g.State == StDone && g.Seed != nil && g.Seed.Status == seed.StSeeding
	})
	g, _ = m.Get(j.ID)
	if g.SeedError != "" || g.Posted == nil || g.Posted.ID != "42" {
		t.Fatalf("après publication : %+v", g)
	}
	if trk.fields.TMDB != 27205 || string(trk.nfo) != "notes" {
		t.Fatalf("envoyé : %+v / %q", trk.fields, trk.nfo)
	}
	site, _ := torrent.Parse(trk.site)
	if strings.EqualFold(site.InfoHash, local) || !strings.EqualFold(site.InfoHash, g.InfoHash) {
		t.Fatalf("seed : local %s, site %s, suivi %s", local, site.InfoHash, g.InfoHash)
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.added["savepath"] != parent || fc.added["stopped"] != "true" || len(fc.started) != 1 {
		t.Fatalf("ajout au client : %v, démarrés %v", fc.added, fc.started)
	}
}

// Une release du même nom déjà sur le site : jamais envoyée.
func TestPublishToTrackerSameName(t *testing.T) {
	_, dir := release(t)
	fc := newFakeClient(t)
	trk := &fakeTracker{existing: []tracker.Version{{ID: "7", Name: "Film 2024 1080p WEB H264-GRP"}, {ID: "8", Name: "Film.2024.2160p.WEB.H265-AUTRE"}}}
	q, _ := client.New(client.Options{URL: fc.srv.URL})
	clients := trackerEnv(trk, seed.Target{ID: "c1", Name: "qBittorrent", Client: q})
	m := New(clients, seed.New(clients), "test")

	j, err := m.Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Existing) != 2 || !j.Existing[0].Same || j.Existing[1].Same {
		t.Fatalf("existants : %+v", j.Existing)
	}
	wait(t, func() bool { g, _ := m.Get(j.ID); return g.State == StReady })
	if err := m.PublishTo(j.ID, tracker.Fields{Name: j.Name}, nil); err == nil {
		t.Fatal("même nom accepté")
	}
	if trk.uploads != 0 {
		t.Fatal("envoi fait malgré le doublon")
	}
}

// Avec un dossier de seed, la release y devient un lien physique au
// nom de la fiche ; le fichier d'origine n'est ni modifié ni renommé, le
// client seede depuis le lien, et le .torrent porte le nom de la fiche.
func TestPublishToTrackerSeedLink(t *testing.T) {
	parent, dir := release(t)
	seedDir := filepath.Join(filepath.Dir(parent), "seed")
	t.Setenv("BARATIE_SEED_DIR", seedDir)
	orig := filepath.Join(dir, "Film.2024.1080p.WEB.H264-GRP.mkv")
	before, _ := os.Stat(orig)
	sum := fileSum(t, orig)

	fc := newFakeClient(t)
	trk := &fakeTracker{}
	q, _ := client.New(client.Options{URL: fc.srv.URL})
	clients := trackerEnv(trk, seed.Target{ID: "c1", Name: "qBittorrent", Client: q})
	m := New(clients, seed.New(clients), "test")
	j, err := m.Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool { g, _ := m.Get(j.ID); return g.State == StReady })
	title := "Film.2024.MULTi.1080p.WEB.H264-GRP"
	if err := m.PublishTo(j.ID, tracker.Fields{Name: title, Description: title, CategoryID: 1, TypeID: 4, ResolutionID: 3, TMDB: 1}, nil); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		g, _ := m.Get(j.ID)
		return g.State == StDone && g.Seed != nil && g.Seed.Status == seed.StSeeding
	})
	g, _ := m.Get(j.ID)
	link := filepath.Join(seedDir, title, "Film.2024.1080p.WEB.H264-GRP.mkv")
	ls, err := os.Stat(link)
	if err != nil || !os.SameFile(ls, before) || g.SeedLink != filepath.Join(seedDir, title) || g.SeedError != "" {
		t.Fatalf("lien %s : %v (%+v)", link, err, g)
	}
	after, err := os.Stat(orig)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() || fileSum(t, orig) != sum {
		t.Fatal("fichier d'origine modifié")
	}
	if fc.added["savepath"] != seedDir {
		t.Fatalf("le client seede depuis %q", fc.added["savepath"])
	}
	site, _ := torrent.Parse(trk.site)
	if site.Name != title {
		t.Fatalf("nom du .torrent : %q", site.Name)
	}
}

// Un fichier étranger déjà à la place du lien : rien n'est écrasé, rien
// n'est envoyé.
func TestPublishToTrackerSeedLinkNoOverwrite(t *testing.T) {
	parent, dir := release(t)
	seedDir := filepath.Join(filepath.Dir(parent), "seed")
	t.Setenv("BARATIE_SEED_DIR", seedDir)
	title := "Film.2024.MULTi.1080p.WEB.H264-GRP"
	other := filepath.Join(seedDir, title, "Film.2024.1080p.WEB.H264-GRP.mkv")
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("autre chose"), 0o644)

	fc := newFakeClient(t)
	trk := &fakeTracker{}
	q, _ := client.New(client.Options{URL: fc.srv.URL})
	clients := trackerEnv(trk, seed.Target{ID: "c1", Name: "qBittorrent", Client: q})
	m := New(clients, seed.New(clients), "test")
	j, _ := m.Start(dir)
	wait(t, func() bool { g, _ := m.Get(j.ID); return g.State == StReady })
	err := m.PublishTo(j.ID, tracker.Fields{Name: title, Description: title, CategoryID: 1, TypeID: 4, ResolutionID: 3, TMDB: 1}, nil)
	if err == nil || trk.uploads != 0 {
		t.Fatalf("écrasement ou envoi : %v, %d envois", err, trk.uploads)
	}
	if b, _ := os.ReadFile(other); string(b) != "autre chose" {
		t.Fatal("fichier étranger écrasé")
	}
	if g, _ := m.Get(j.ID); g.State != StReady || !strings.Contains(g.Error, "rien n'est écrasé") {
		t.Fatalf("état : %+v", g)
	}
}

// Un fichier seul devient « Titre.ext » ; un titre piégé ne sort jamais du
// dossier de seed.
func TestSeedPairs(t *testing.T) {
	plan := &maketorrent.Plan{Name: "Inception (2010).mkv", Single: true, Files: []maketorrent.Entry{{Rel: "Inception (2010).mkv", Abs: "/data/media/Cinéma/Inception (2010)/Inception (2010).mkv", Size: 10}}}
	name, pairs, err := seedPairs("/data/downloads/baratie", plan, "Inception.2010.MULTi.1080p.BluRay.x264-GRP")
	if err != nil || name != "Inception.2010.MULTi.1080p.BluRay.x264-GRP.mkv" || pairs[0].Dst != "/data/downloads/baratie/Inception.2010.MULTi.1080p.BluRay.x264-GRP.mkv" {
		t.Fatalf("%q %+v %v", name, pairs, err)
	}
	for _, evil := range []string{"../../etc/passwd", "a/../../b", "..", "/abs/olu", `x\\..\\y`} {
		_, pairs, err := seedPairs("/data/downloads/baratie", plan, evil)
		if err == nil && !strings.HasPrefix(pairs[0].Dst, "/data/downloads/baratie/") {
			t.Fatalf("%q sort du dossier : %s", evil, pairs[0].Dst)
		}
		if err == nil && strings.Contains(strings.TrimPrefix(pairs[0].Dst, "/data/downloads/baratie/"), "/") {
			t.Fatalf("%q crée un sous-dossier : %s", evil, pairs[0].Dst)
		}
	}
}

func fileSum(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
