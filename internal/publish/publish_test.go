package publish

import (
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/V3x0rV3x/D3X/internal/bencode"
	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/crossseed"
	"github.com/V3x0rV3x/D3X/internal/qbit"
	"github.com/V3x0rV3x/D3X/internal/source"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// Parcours complet contre un faux qBittorrent et un faux V3X : liste, hachage,
// publication, .torrent de l'uploadeur, ajout à l'arrêt, vérification, seed.
func TestPublishFlow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	parent := filepath.Join(tmp, "Releases")
	dir := filepath.Join(parent, "Film.2024.1080p.WEB.H264-D3X")
	os.MkdirAll(dir, 0o755)
	data := make([]byte, 5<<20)
	rand.New(rand.NewSource(1)).Read(data)
	os.WriteFile(filepath.Join(dir, "Film.2024.1080p.WEB.H264-D3X.mkv"), data, 0o644)
	os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644)

	// Faux qBittorrent : ajout, vérification en deux tours, démarrage.
	var mu sync.Mutex
	type ent struct {
		qbit.Torrent
		left int
	}
	list := map[string]*ent{}
	var added map[string]string
	var started []string
	qs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "x", Path: "/"})
			io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			io.WriteString(w, "v5.0.0")
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.0")
		case "/api/v2/torrents/info":
			out := []qbit.Torrent{}
			for h, e := range list {
				if q := r.Form.Get("hashes"); q != "" && !strings.Contains(q, h) {
					continue
				}
				if e.left > 0 {
					e.left--
					e.State = "checkingUP"
					if e.left == 0 {
						e.State, e.Progress = "stoppedUP", 1
					}
				}
				out = append(out, e.Torrent)
			}
			json.NewEncoder(w).Encode(out)
		case "/api/v2/torrents/add":
			r.ParseMultipartForm(1 << 20)
			added = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				added[k] = v[0]
			}
			fh := r.MultipartForm.File["torrents"][0]
			fd, _ := fh.Open()
			raw, _ := io.ReadAll(fd)
			p, _ := torrent.Parse(raw)
			h := strings.ToLower(p.InfoHash)
			list[h] = &ent{Torrent: qbit.Torrent{Hash: h, Name: p.Name, State: "stoppedDL"}}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/recheck":
			if e := list[strings.ToLower(r.Form.Get("hashes"))]; e != nil {
				e.left = 2
			}
		case "/api/v2/torrents/start":
			started = append(started, r.Form.Get("hashes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer qs.Close()

	// Faux V3X : aucun doublon, publication acceptée, .torrent de l'uploadeur.
	var uploaded []byte
	var upFields map[string]string
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/d3x/match":
			io.WriteString(w, `{"results":[{"key":"0","match":null}]}`)
		case r.URL.Path == "/api/torrents":
			r.ParseMultipartForm(8 << 20)
			upFields = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				upFields[k] = v[0]
			}
			f, _, _ := r.FormFile("file")
			uploaded, _ = io.ReadAll(f)
			p, _ := torrent.Parse(uploaded)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"id": "aaaaaaaa-0000-4000-8000-000000000001", "status": "active", "infoHash": p.InfoHash, "uploadBonus": 400})
		case strings.HasSuffix(r.URL.Path, "/download"):
			d, _ := bencode.Decode(uploaded)
			root := d.Root.(bencode.Dict)
			root["announce"] = []byte("https://announce.v3x.club/pk/announce")
			b, _ := bencode.Encode(root)
			w.Write(b)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()

	q, _ := client.New(client.Options{URL: qs.URL})
	v := v3x.New(vs.URL, "cle", "D3X/test")
	cfg := config.Defaults()
	clients := func() (client.Client, *v3x.Client, *config.Config, error) { return q, v, cfg, nil }
	eng := crossseed.New(crossseed.Clients(clients))
	m := New(Clients(clients), eng, "test")

	j, err := m.Start(source.Local{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if j.Duplicate != nil || len(j.Files) != 1 || j.MainFile != 0 || len(j.Skipped) != 1 {
		t.Fatalf("liste : %+v", j)
	}
	wait(t, func() bool { g, _ := m.Get(j.ID); return g.State == StReady })
	g, _ := m.Get(j.ID)
	if g.InfoHash == "" {
		t.Fatal("info-hash attendu dès la fin du hachage")
	}
	if err := m.Publish(j.ID, map[string]string{"categoryId": "11", "releaseName": "Film.2024.1080p.WEB.H264-D3X"}); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool {
		g, _ := m.Get(j.ID)
		return g.State == StDone && g.Seed != nil && g.Seed.Status == crossseed.StSeeding
	})
	g, _ = m.Get(j.ID)
	if g.SeedError != "" {
		t.Fatalf("seed : %s", g.SeedError)
	}
	if upFields["categoryId"] != "11" {
		t.Fatalf("champs envoyés : %v", upFields)
	}
	mu.Lock()
	defer mu.Unlock()
	if added["savepath"] != parent || added["stopped"] != "true" || added["skip_checking"] != "false" {
		t.Fatalf("ajout au client : %v", added)
	}
	p, _ := torrent.Parse(uploaded)
	if !strings.EqualFold(p.InfoHash, g.InfoHash) || len(started) != 1 {
		t.Fatalf("info-hash publié %s, suivi %s, démarrés %v", p.InfoHash, g.InfoHash, started)
	}
}

func wait(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("délai dépassé")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Les dossiers à secrets ne se publient pas.
func TestPrivateFolders(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	dir, _ := config.Dir()
	for _, p := range []string{filepath.Join(dir, "config.json"), dir, "/home/membre/.ssh/id_ed25519", `C:\Users\x\.ssh`, "/home/membre/.d3x/agent"} {
		if private(source.Local{}, p) == nil {
			t.Errorf("%s accepté", p)
		}
	}
	for _, p := range []string{"/data/Films/Film.2020", `D:\Releases\Album`} {
		if err := private(source.Local{}, p); err != nil {
			t.Errorf("%s refusé : %v", p, err)
		}
	}
}
