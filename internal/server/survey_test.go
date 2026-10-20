package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/V3x0rV3x/D3X/internal/bencode"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/qbit"
	"github.com/V3x0rV3x/D3X/internal/torrent"
)

// La surveillance : le premier passage pose l'état de départ (rien n'est
// ajouté), le suivant ajoute seul la release identique apparue entre-temps.
func TestSurveyAutoAdd(t *testing.T) {
	tmp, err := os.MkdirTemp("", "d3x-survey")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	mk := func(name, src string, size int64) []byte {
		info := bencode.Dict{"name": []byte(name), "piece length": int64(1 << 22), "files": []bencode.Value{bencode.Dict{"length": size, "path": []bencode.Value{[]byte(name + ".mkv")}}}, "pieces": make([]byte, (size+(1<<22)-1)/(1<<22)*20), "private": int64(1), "source": []byte(src)}
		b, _ := bencode.Encode(bencode.Dict{"announce": []byte("https://tracker.invalid/announce"), "info": info})
		return b
	}
	type rel struct {
		local, v3x []byte
		id         string
	}
	rels := map[string]rel{}
	for i, n := range []string{"Premiere.2020.1080p.WEB-GRP", "Seconde.2021.1080p.WEB-GRP"} {
		rels[n] = rel{mk(n, "AUTRE", int64(900_000_000+i)), mk(n, "V3X", int64(900_000_000+i)), []string{"0000000a-0000-4000-8000-00000000000a", "0000000b-0000-4000-8000-00000000000b"}[i]}
	}

	var mu sync.Mutex
	inClient := map[string]qbit.Torrent{}
	raws := map[string][]byte{}
	var added []string
	put := func(raw []byte, save string, progress float64, state string) {
		p, _ := torrent.Parse(raw)
		h := strings.ToLower(p.InfoHash)
		inClient[h] = qbit.Torrent{Hash: h, Name: p.Name, SavePath: save, ContentPath: save + "/" + p.Name, Progress: progress, State: state, Size: p.Total, TotalSize: p.Total}
		raws[h] = raw
	}
	put(rels["Premiere.2020.1080p.WEB-GRP"].local, "/data", 1, "stalledUP")

	qs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "x", Path: "/"})
			io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			io.WriteString(w, "v5.1.0")
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.4")
		case "/api/v2/torrents/info":
			want := map[string]bool{}
			for _, h := range strings.Split(r.Form.Get("hashes"), "|") {
				if h != "" {
					want[strings.ToLower(h)] = true
				}
			}
			out := []qbit.Torrent{}
			for h, x := range inClient {
				if len(want) == 0 || want[h] {
					out = append(out, x)
				}
			}
			json.NewEncoder(w).Encode(out)
		case "/api/v2/torrents/export":
			if b := raws[strings.ToLower(r.Form.Get("hash"))]; b != nil {
				w.Write(b)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case "/api/v2/torrents/add":
			r.ParseMultipartForm(1 << 20)
			fd, _ := r.MultipartForm.File["torrents"][0].Open()
			raw, _ := io.ReadAll(fd)
			p, _ := torrent.Parse(raw)
			added = append(added, p.Name)
			put(raw, r.MultipartForm.Value["savepath"][0], 0, "stoppedDL")
			io.WriteString(w, "Ok.")
		default:
			io.WriteString(w, "Ok.")
		}
	}))
	defer qs.Close()

	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"membre","ratio":1,"hitAndRun":0}`)
		case "/api/d3x/match":
			var in struct {
				Items []struct{ Key, ContentHash string }
			}
			json.NewDecoder(r.Body).Decode(&in)
			var out []map[string]interface{}
			for _, it := range in.Items {
				res := map[string]interface{}{"key": it.Key, "match": nil}
				for _, x := range rels {
					l, _ := torrent.Parse(x.local)
					v, _ := torrent.Parse(x.v3x)
					if l.ContentHash == it.ContentHash {
						res["match"] = map[string]interface{}{"kind": "exact", "id": x.id, "name": v.Name, "infoHash": v.InfoHash, "seeders": 1, "size": v.Total}
					}
				}
				out = append(out, res)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"results": out})
		case "/torznab/download":
			for _, x := range rels {
				if x.id == r.URL.Query().Get("id") {
					w.Write(x.v3x)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case "/api/d3x/seed-status":
			io.WriteString(w, `{"hnr":{"items":[]},"relays":{"items":[]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()

	cfg := config.Defaults()
	cfg.V3XBase, cfg.V3XKey = vs.URL, "cle"
	cfg.QbitURL, cfg.QbitUser, cfg.QbitPass = qs.URL, "admin", "x"
	cfg.Watch = config.Watch{On: true, AutoAdd: true}
	s := New("test", cfg, "jeton", 0)
	for i := 0; i < 50; i++ {
		if _, _, _, err := s.clients(); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	ctx := context.Background()

	// Premier passage : la release déjà là n'est ni « nouvelle » ni ajoutée.
	if out := s.surveyRun(ctx, cfg.Watch); out.Error != "" || out.New != 0 || out.Added != 0 {
		t.Fatalf("premier passage : %+v", out)
	}
	// Une nouvelle release arrive dans le client.
	mu.Lock()
	put(rels["Seconde.2021.1080p.WEB-GRP"].local, "/data", 1, "stalledUP")
	mu.Unlock()
	out := s.surveyRun(ctx, cfg.Watch)
	if out.Error != "" || out.New != 1 || out.Added != 1 {
		t.Fatalf("second passage : %+v", out)
	}
	for i := 0; i < 100; i++ {
		mu.Lock()
		n := len(added)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(added) != 1 || added[0] != "Seconde.2021.1080p.WEB-GRP" {
		for _, r := range s.engine.Rows() {
			t.Logf("%s %s %s", r.Name, r.Status, r.Detail)
		}
		t.Fatalf("ajouts : %v", added)
	}
	// Troisième passage : plus rien de nouveau.
	mu.Unlock()
	out = s.surveyRun(ctx, cfg.Watch)
	mu.Lock()
	if out.New != 0 {
		t.Fatalf("troisième passage : %+v", out)
	}
}
