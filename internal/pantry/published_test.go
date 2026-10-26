package pantry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"baratie/internal/config"
	"baratie/internal/fiche"
	"baratie/internal/tracker"
	"baratie/internal/unit3d/fake"
)

func TestMarkPublished(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media", "Cinéma")
	film := filepath.Join(media, "Film (2001)", "Film (2001) Bluray-1080p.mkv")
	other := filepath.Join(media, "Film (2001)", "Film (2001) WEBDL-720p.mkv")
	mk(t, film, big)
	mk(t, other, big+10)
	// Publié depuis downloads : un lien physique du même fichier.
	dl := filepath.Join(root, "downloads", "Film.2001.FRENCH.1080p.BluRay.x264-GRP.mkv")
	os.MkdirAll(filepath.Dir(dl), 0o755)
	if err := os.Link(film, dl); err != nil {
		t.Skip("liens physiques impossibles ici")
	}
	before, _ := os.Stat(film)

	m := &Manager{deps: Deps{Cfg: func() config.Config { return config.Config{PantryLibs: []string{media}} }, Tracker: func() tracker.Tracker { return nil }}, items: map[string]*Item{}}
	for _, p := range []string{film, other} {
		st, _ := os.Stat(p)
		it := &Item{Found: Found{ID: IDOf(p), Path: p, Size: st.Size()}, Name: filepath.Base(p), TMDB: 42, CheckedAt: time.Now()}
		m.items[it.ID] = it
	}
	m.MarkPublished(Posted{TMDB: 42, Name: "Film.2001.FRENCH.1080p.BluRay.x264-GRP", URL: "https://tracker.invalid/t/1", Paths: []string{dl}, Sizes: []int64{big}, Category: "Films", Type: "Encode", Resolution: "1080p"})

	pub := m.items[IDOf(film)]
	if pub.Class != ClassIdentical || !strings.Contains(pub.Why, "publiée par toi") || pub.Published == "" {
		t.Fatalf("film publié : %s %q", pub.Class, pub.Why)
	}
	o := m.items[IDOf(other)]
	if len(o.Versions) != 1 || o.Class == ClassAbsent {
		t.Fatalf("autre copie : %s %q %v", o.Class, o.Why, o.Versions)
	}
	after, _ := os.Stat(film)
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Fatal("fichier modifié")
	}
}

func TestClassifyProbe(t *testing.T) {
	rules := fake.Rules().Fiche()
	vo := &fiche.MediaInfo{Container: "Matroska", VideoFmt: "AVC", Width: 1920, Height: 800, AudioLangs: []string{"English"}, Audios: []fiche.Audio{{Lang: "English", Format: "DTS", Channels: 6}}}
	it := &Item{Found: Found{Path: "/m/Film.2001.VOSTFR.1080p.BluRay.x264-GRP.mkv", Size: big}, Name: "Film.2001.VOSTFR.1080p.BluRay.x264-GRP", TMDB: 1, Probe: vo}
	if c, why := Classify(it, rules); c != ClassExcluded || !strings.Contains(why, "VO sans sous-titres") {
		t.Fatalf("%s %q", c, why)
	}
	// Nom sans langue ni résolution : le fichier les donne.
	fr := &fiche.MediaInfo{Container: "Matroska", VideoFmt: "AVC", Width: 1280, Height: 720, AudioLangs: []string{"French"}, Audios: []fiche.Audio{{Lang: "French", Format: "AC-3", Channels: 6}}}
	it = &Item{Found: Found{Path: "/m/Film (2001).mkv", Size: big}, Name: "Film (2001)", TMDB: 1, Probe: fr, CheckedAt: time.Now(),
		Versions: []tracker.Version{{Name: "Film.2001.FRENCH.1080p.WEB.H264-X", Resolution: "1080p", Type: "WEB", Category: "Films", Size: 2 << 30}}}
	if c, why := Classify(it, rules); c != ClassComplete {
		t.Fatalf("%s %q", c, why)
	}
}
