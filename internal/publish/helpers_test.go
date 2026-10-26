package publish

import (
	"context"
	"encoding/json"
	"errors"
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

	"baratie/internal/client"
	"baratie/internal/config"
	"baratie/internal/pathmap"
	"baratie/internal/qbit"
	"baratie/internal/seed"
	"baratie/internal/torrent"
)

type fakeClient struct {
	mu      sync.Mutex
	list    map[string]*fakeEnt
	added   map[string]string
	started []string
	srv     *httptest.Server
}

type fakeEnt struct {
	qbit.Torrent
	left int
}

func newFakeClient(t *testing.T) *fakeClient {
	f := &fakeClient{list: map[string]*fakeEnt{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
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
			for h, e := range f.list {
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
			f.added = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				f.added[k] = v[0]
			}
			fh := r.MultipartForm.File["torrents"][0]
			fd, _ := fh.Open()
			raw, _ := io.ReadAll(fd)
			p, _ := torrent.Parse(raw)
			h := strings.ToLower(p.InfoHash)
			f.list[h] = &fakeEnt{Torrent: qbit.Torrent{Hash: h, Name: p.Name, State: "stoppedDL"}}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/recheck":
			if e := f.list[strings.ToLower(r.Form.Get("hashes"))]; e != nil {
				e.left = 2
			}
		case "/api/v2/torrents/start":
			f.started = append(f.started, r.Form.Get("hashes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// fakeSite : un faux tracker sans doublon, qui accepte la publication et rend le
// .torrent de l'uploadeur.
func release(t *testing.T) (parent, dir string) {
	tmp := t.TempDir()
	t.Setenv("BARATIE_CONFIG", filepath.Join(tmp, "config"))
	t.Setenv("HOME", tmp)
	parent = filepath.Join(tmp, "Releases")
	dir = filepath.Join(parent, "Film.2024.1080p.WEB.H264-GRP")
	os.MkdirAll(dir, 0o755)
	data := make([]byte, 5<<20)
	rand.New(rand.NewSource(1)).Read(data)
	os.WriteFile(filepath.Join(dir, "Film.2024.1080p.WEB.H264-GRP.mkv"), data, 0o644)
	os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644)
	return parent, dir
}

// listOnly : un client qui ne sait que lister.
type listOnly struct {
	client.Client
	list []client.Torrent
}

func (l listOnly) List(context.Context, []string) ([]client.Torrent, error) { return l.list, nil }

type downClient struct{ client.Client }

func (downClient) List(context.Context, []string) ([]client.Torrent, error) {
	return nil, errors.New("injoignable")
}

// Le client choisi par défaut : règle qui couvre le dossier, sinon celui qui
// seede déjà dans ce dossier, sinon celui de la machine des fichiers, sinon
func TestPick(t *testing.T) {
	parent := t.TempDir()
	os.MkdirAll(filepath.Join(parent, "Deja.La"), 0o755)
	seeds := listOnly{list: []client.Torrent{{ContentPath: "/srv/rel/Deja.La"}}}
	empty := listOnly{}
	env := func(ts ...seed.Target) *seed.Env {
		return &seed.Env{Cfg: config.Defaults(), Targets: ts}
	}
	ctx := context.Background()
	cases := []struct {
		name    string
		env     *seed.Env
		id, dir string
	}{
		{"règle", env(seed.Target{ID: "a", Client: seeds}, seed.Target{ID: "b", Client: empty, PathMap: []pathmap.Rule{{From: parent, To: "/b"}}}), "b", "/b"},
		{"seede déjà là", env(seed.Target{ID: "a", Client: empty, Host: "127.0.0.1"}, seed.Target{ID: "b", Client: seeds}), "b", "/srv/rel"},
		{"même machine", env(seed.Target{ID: "a", Client: empty, Host: "box.exemple.net"}, seed.Target{ID: "b", Client: downClient{}, Host: "localhost"}), "b", parent},
		{"le premier", env(seed.Target{ID: "a", Client: empty, Host: "box.exemple.net"}, seed.Target{ID: "b", Client: empty}), "a", parent},
	}
	for _, c := range cases {
		p := pick(ctx, c.env, parent)
		if p.t.ID != c.id || p.dir != c.dir {
			t.Errorf("%s : client %q dossier %q, attendu %q %q", c.name, p.t.ID, p.dir, c.id, c.dir)
		}
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
	t.Setenv("BARATIE_CONFIG", tmp)
	dir, _ := config.Dir()
	for _, p := range []string{filepath.Join(dir, "config.json"), dir, "/home/membre/.ssh/id_ed25519", `C:\Users\x\.ssh`} {
		if private(p) == nil {
			t.Errorf("%s accepté", p)
		}
	}
	for _, p := range []string{"/data/Films/Film.2020", `D:\Releases\Album`} {
		if err := private(p); err != nil {
			t.Errorf("%s refusé : %v", p, err)
		}
	}
}
