package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/V3x0rV3x/D3X/internal/bencode"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/maketorrent"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/qbit"
	"github.com/V3x0rV3x/D3X/internal/remote/sshtest"
	"github.com/V3x0rV3x/D3X/internal/torrent"
)

// TestDemo lance la page avec un faux qBittorrent et un faux V3X, peuplés de
// releases aux noms neutres : pour regarder l'interface et faire les captures
// du README sans compte réel.
//
//	D3X_DEMO=600 go test ./internal/server -run TestDemo -v
//
// (D3X_DEMO_ROOT=C:\Medias pour des chemins neutres ; D3X_DEMO_SSH=1 pour une
// seedbox de démonstration.)
//
// puis ouvrir http://127.0.0.1:8799/auth?t=demo
func TestDemo(t *testing.T) {
	secs, _ := strconv.Atoi(os.Getenv("D3X_DEMO"))
	if secs <= 0 {
		t.Skip("D3X_DEMO non défini")
	}
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	type rel struct {
		name    string
		files   map[string]int64
		onV3X   bool // déjà présent au catalogue
		none    bool // pas sur V3X : ses fichiers existent, pour Publier et Lot
		seeders int
		renamed bool
		ok      float64
		disk    bool // sur V3X et sur le disque, pas dans le client
	}
	var rels []rel
	titles := []string{"Exemple", "Demonstration", "Echantillon", "Specimen", "Prototype", "Maquette", "Brouillon", "Essai", "Modele", "Esquisse", "Croquis", "Epreuve"}
	for i, ti := range titles {
		for j, q := range []string{"1080p.WEB.H264", "2160p.WEB.H265", "720p.HDTV.x264"} {
			n := fmt.Sprintf("%s.%d.%s-DEMO", ti, 2001+i, q)
			if i == 1 && j == 1 {
				n = strings.TrimSuffix(n, "-DEMO") + "-BANNI" // groupe refusé sur V3X (1.1.28)
			}
			size := int64(700_000_000 + (i*3+j)*413_000_017)
			r := rel{name: n, files: map[string]int64{n + ".mkv": size, n + ".nfo": 3_200}, seeders: (i*7 + j*3) % 9, ok: 1}
			switch (i*3 + j) % 6 {
			case 0, 1, 2:
				r.onV3X = true
			case 3:
				r.renamed = true
			case 4:
				// pas sur V3X : petite, pour que ses fichiers existent vraiment
				r.none = true
				small := int64(8_000_000 + i*1_700_000)
				r.files = map[string]int64{n + ".mkv": small, n + ".nfo": 3_200}
			case 5:
				r.onV3X = true
				r.ok = 0.62
			}
			rels = append(rels, r)
		}
	}
	// Déjà sur V3X, posée sur le disque mais absente du client (Turias,
	// 22/10/26) : Publier la met en seed avec ces fichiers.
	dup := "Duplicata.2024.1080p.WEB.H264-DEMO"
	rels = append(rels, rel{name: dup, files: map[string]int64{dup + ".mkv": 6_000_000, dup + ".nfo": 3_200}, seeders: 4, ok: 1, onV3X: true, disk: true})

	mk := func(name string, files map[string]int64, src string) []byte {
		const pl = int64(1 << 22)
		var list []bencode.Value
		var total int64
		for p, n := range files {
			list = append(list, bencode.Dict{"length": n, "path": []bencode.Value{[]byte(p)}})
			total += n
		}
		info := bencode.Dict{"name": []byte(name), "piece length": pl, "files": list, "pieces": make([]byte, (total+pl-1)/pl*20), "private": int64(1), "source": []byte(src)}
		b, _ := bencode.Encode(bencode.Dict{"announce": []byte("https://tracker.invalid/announce"), "info": info})
		return b
	}

	type entry struct {
		qbit.Torrent
		raw   []byte
		check time.Time // fin de la vérification en cours
		after float64
		names map[int]string // noms renommés dans le client
	}
	var mu sync.Mutex
	client := map[string]*entry{}
	catalog := map[string]struct {
		raw     []byte
		id      string
		seeders int
		renamed bool
		ok      float64
	}{}
	byID := map[string][]byte{}
	libSizes := map[int64]map[string]interface{}{} // médiathèque : taille → release V3X
	var relays []map[string]interface{}
	var hnr []map[string]interface{}
	for i, r := range rels {
		local := mk(r.name, r.files, "AUTRE")
		p, _ := torrent.Parse(local)
		h := strings.ToLower(p.InfoHash)
		if !r.disk {
			client[h] = &entry{Torrent: qbit.Torrent{Hash: h, Name: r.name, SavePath: "/data/torrents", ContentPath: "/data/torrents/" + r.name, Progress: 1, State: "stalledUP", Size: p.Total, TotalSize: p.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2Ftracker.invalid%2Fannounce"}, raw: local}
		}
		if r.onV3X || r.renamed {
			name := r.name
			if r.renamed {
				name = strings.ReplaceAll(r.name, ".", " ")
			}
			files := map[string]int64{}
			for f, n := range r.files {
				if r.renamed {
					f = strings.ReplaceAll(f, ".", " ")
				}
				files[f] = n
			}
			v := mk(name, files, "V3X")
			id := fmt.Sprintf("%08x-0000-4000-8000-%012x", i+1, i+1)
			catalog[p.ContentHash] = struct {
				raw     []byte
				id      string
				seeders int
				renamed bool
				ok      float64
			}{v, id, r.seeders, r.renamed, r.ok}
			if r.renamed {
				catalog[p.SizeHash] = catalog[p.ContentHash]
				delete(catalog, p.ContentHash)
			}
			byID[id] = v
			// Deux relais ouverts, parmi les releases rares à ajouter.
			if pv, _ := torrent.Parse(v); r.onV3X && r.ok == 1 && r.seeders == 1 && len(relays) < 2 {
				relays = append(relays, map[string]interface{}{"infoHash": pv.InfoHash, "torrentId": id, "name": pv.Name, "seeders": 1, "until": time.Now().Add(52 * time.Hour)})
			}
		}
	}
	// Une release déjà seedée pour V3X.
	mine := mk("Deja.Seede.Pour.V3X.2020.1080p-DEMO", map[string]int64{"Deja.mkv": 1_234_567_890}, "V3X")
	pm, _ := torrent.Parse(mine)
	hnr = append(hnr,
		map[string]interface{}{"infoHash": pm.InfoHash, "torrentId": "0000000a-0000-4000-8000-00000000000a", "name": "Deja.Seede.Pour.V3X.2020.1080p-DEMO", "seedSeconds": 31 * 3600, "requiredSeconds": 48 * 3600, "deadline": time.Now().Add(9 * 24 * time.Hour), "seeders": 4, "seedingNow": true},
		map[string]interface{}{"infoHash": strings.Repeat("ab", 20), "torrentId": "0000000b-0000-4000-8000-00000000000b", "name": "Oubliee.Dans.Un.Coin.2019.1080p.WEB-DEMO", "seedSeconds": 5 * 3600, "requiredSeconds": 48 * 3600, "deadline": time.Now().Add(50 * time.Hour), "seeders": 2},
	)
	client[strings.ToLower(pm.InfoHash)] = &entry{Torrent: qbit.Torrent{Hash: strings.ToLower(pm.InfoHash), Name: "Deja.Seede.Pour.V3X.2020.1080p-DEMO", SavePath: "/data/v3x", ContentPath: "/data/v3x/Deja.Seede.Pour.V3X.2020.1080p-DEMO", Progress: 1, State: "stalledUP", Size: pm.Total, TotalSize: pm.Total, MagnetURI: "magnet:?xt=urn:btih:x&tr=https%3A%2F%2Fannounce.v3x.club%2Fpk%2Fannounce"}, raw: mine}

	// D3X_DEMO_CLIENTS=1 : trois clients, la seedbox (première des réglages),
	// ce PC et un NAS éteint, pour les étiquettes, les pastilles et le
	// bandeau « injoignable ». La seedbox prend une release sur trois (jamais
	// les « pas sur V3X », dont les fichiers sont sur ce PC), et en partage
	// une avec ce PC.
	multi := os.Getenv("D3X_DEMO_CLIENTS") != ""
	box := map[string]*entry{}
	if multi {
		none := map[string]bool{}
		for _, r := range rels {
			none[r.name] = r.none
		}
		var hs []string
		for h := range client {
			hs = append(hs, h)
		}
		sort.Strings(hs)
		shared := false
		for i, h := range hs {
			e := client[h]
			if none[e.Name] || i%3 != 0 {
				continue
			}
			c := *e
			c.SavePath, c.ContentPath = "/home/membre/torrents", "/home/membre/torrents/"+e.Name
			box[h] = &c
			if !shared && strings.Contains(e.Name, "1080p") {
				shared = true
				continue
			}
			delete(client, h)
		}
	}

	serve := func(client map[string]*entry) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				for _, x := range strings.Split(r.Form.Get("hashes"), "|") {
					if x != "" {
						want[strings.ToLower(x)] = true
					}
				}
				out := []qbit.Torrent{}
				for h, e := range client {
					if len(want) > 0 && !want[h] {
						continue
					}
					// Vérification simulée : 2,5 s, au temps écoulé.
					if !e.check.IsZero() {
						if left := time.Until(e.check); left > 0 {
							e.State, e.Progress = "checkingUP", e.after*(1-left.Seconds()/2.5)
						} else {
							e.check = time.Time{}
							e.Progress, e.State = e.after, "stoppedDL"
							if e.after >= 1 {
								e.State = "stoppedUP"
							}
						}
					}
					out = append(out, e.Torrent)
				}
				json.NewEncoder(w).Encode(out)
			case "/api/v2/torrents/files":
				e := client[strings.ToLower(r.Form.Get("hash"))]
				if e == nil {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				p, _ := torrent.Parse(e.raw)
				type file struct {
					Index int    `json:"index"`
					Name  string `json:"name"`
					Size  int64  `json:"size"`
				}
				out := []file{}
				for _, f := range p.Files {
					n := f.Path
					if x, ok := e.names[f.Index]; ok {
						n = x
					}
					out = append(out, file{f.Index, n, f.Size})
				}
				json.NewEncoder(w).Encode(out)
			case "/api/v2/app/getDirectoryContent":
				// Disque simulé : les emplacements des torrents du faux client.
				dir := strings.TrimRight(r.Form.Get("dirPath"), "/") + "/"
				seen := map[string]bool{}
				out := []string{}
				for _, e := range client {
					if strings.HasPrefix(e.ContentPath, dir) {
						name := strings.SplitN(e.ContentPath[len(dir):], "/", 2)[0]
						if !seen[name] {
							seen[name] = true
							out = append(out, dir+name)
						}
					}
				}
				if len(out) == 0 {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				json.NewEncoder(w).Encode(out)
			case "/api/v2/torrents/renameFile":
				e := client[strings.ToLower(r.Form.Get("hash"))]
				if e == nil {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				p, _ := torrent.Parse(e.raw)
				if e.names == nil {
					e.names = map[int]string{}
				}
				for _, f := range p.Files {
					cur := f.Path
					if x, ok := e.names[f.Index]; ok {
						cur = x
					}
					if cur == r.Form.Get("oldPath") {
						e.names[f.Index] = r.Form.Get("newPath")
						if i := strings.IndexByte(r.Form.Get("newPath"), '/'); i > 0 {
							e.ContentPath = e.SavePath + "/" + r.Form.Get("newPath")[:i]
						}
					}
				}
			case "/api/v2/torrents/export":
				time.Sleep(15 * time.Millisecond)
				if e := client[strings.ToLower(r.Form.Get("hash"))]; e != nil {
					w.Write(e.raw)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			case "/api/v2/torrents/add":
				r.ParseMultipartForm(1 << 20)
				fh := r.MultipartForm.File["torrents"][0]
				fd, _ := fh.Open()
				raw, _ := io.ReadAll(fd)
				p, _ := torrent.Parse(raw)
				h := strings.ToLower(p.InfoHash)
				after := 1.0
				for _, c := range catalog {
					if string(c.raw) == string(raw) {
						after = c.ok
					}
				}
				client[h] = &entry{Torrent: qbit.Torrent{Hash: h, Name: p.Name, SavePath: r.MultipartForm.Value["savepath"][0], ContentPath: r.MultipartForm.Value["savepath"][0] + "/" + p.Name, State: "stoppedDL", Size: p.Total, TotalSize: p.Total}, raw: raw, after: after}
				io.WriteString(w, "Ok.")
			case "/api/v2/torrents/recheck":
				if e := client[strings.ToLower(r.Form.Get("hashes"))]; e != nil {
					e.check = time.Now().Add(2500 * time.Millisecond)
				}
			case "/api/v2/torrents/delete":
				delete(client, strings.ToLower(r.Form.Get("hashes")))
			case "/api/v2/torrents/start":
				if e := client[strings.ToLower(r.Form.Get("hashes"))]; e != nil {
					e.State = "stalledUP"
				}
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
	}
	qs := httptest.NewServer(serve(client))
	defer qs.Close()
	bs := httptest.NewServer(serve(box))
	defer bs.Close()

	dp := &demoPub{uploaded: map[string][]byte{}}
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dp.handle(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"membre-demo","ratio":3.42,"hitAndRun":0}`)
		case "/api/d3x/match":
			var in struct {
				Items []struct{ Key, InfoHash, ContentHash, SizeHash string }
			}
			json.NewDecoder(r.Body).Decode(&in)
			var out []map[string]interface{}
			for _, it := range in.Items {
				res := map[string]interface{}{"key": it.Key, "match": nil}
				c, ok := catalog[it.ContentHash]
				if !ok {
					c, ok = catalog[it.SizeHash]
				}
				if ok {
					p, _ := torrent.Parse(c.raw)
					kind := "exact"
					if c.renamed {
						kind = "renamed"
					}
					res["match"] = map[string]interface{}{"kind": kind, "id": c.id, "name": p.Name, "infoHash": p.InfoHash, "seeders": c.seeders, "leechers": c.seeders % 3, "size": p.Total, "dead": c.seeders == 0}
				}
				out = append(out, res)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"results": out, "reseedPrime": 2000})
		case "/api/d3x/match-files":
			var in struct{ Sizes []int64 }
			json.NewDecoder(r.Body).Decode(&in)
			out := map[string][]map[string]interface{}{}
			for _, n := range in.Sizes {
				if m := libSizes[n]; m != nil {
					out[strconv.FormatInt(n, 10)] = []map[string]interface{}{m}
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"matches": out})
		case "/api/d3x/team-policies":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"teams":      []interface{}{},
				"bannedTags": []map[string]string{{"tag": "BANNI", "reason": "Reprend les releases d'autres groupes en retirant leur tag"}},
			})
		case "/api/d3x/seed-status":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"hnr":    map[string]interface{}{"enabled": true, "minSeedHours": 48, "windowDays": 14, "maxActive": 3, "active": 0, "items": hnr},
				"relays": map[string]interface{}{"prime": 500, "maxPaid": 2, "sustainHours": 24, "items": relays},
			})
		case "/api/d3x/requests":
			json.NewEncoder(w).Encode(map[string]interface{}{"requests": []map[string]interface{}{
				{"id": "0000000c-0000-4000-8000-00000000000c", "title": "Specimen (2004)", "year": "2004", "mediaType": "movie", "reward": 1500},
				{"id": "0000000d-0000-4000-8000-00000000000d", "title": "Essai (2008)", "year": "2008", "mediaType": "movie", "reward": 400},
				{"id": "0000000e-0000-4000-8000-00000000000e", "title": "Une Autre Oeuvre (1999)", "year": "1999", "mediaType": "movie", "reward": 900},
			}})
		case "/api/d3x/requests/0000000c-0000-4000-8000-00000000000c/propose", "/api/d3x/requests/0000000d-0000-4000-8000-00000000000d/propose":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"id":"0000000f-0000-4000-8000-00000000000f"}`)
		case "/torznab/download":
			if b := byID[r.URL.Query().Get("id")]; b != nil {
				w.Write(b)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()

	demoMusicBrainz(t)
	cfg := config.Defaults()
	// D3X_DEMO_ROOT : dossier des releases de démonstration aux chemins
	// neutres, pour les captures du README (vidé avant et après).
	media := tmp
	if r := os.Getenv("D3X_DEMO_ROOT"); r != "" {
		media = r
		_ = os.RemoveAll(filepath.Join(r, "Releases"))
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(r, "Releases")) })
	}
	cfg.LastDir = demoRelease(media)
	// Les releases « pas sur V3X » : vues ici dans Seed, par le client dans
	// /data/torrents.
	seed := filepath.Join(media, "Seed")
	_ = os.RemoveAll(seed)
	t.Cleanup(func() { _ = os.RemoveAll(seed) })
	for _, r := range rels {
		if !r.none && !r.disk {
			continue
		}
		dir := filepath.Join(seed, r.name)
		_ = os.MkdirAll(dir, 0o755)
		for f, n := range r.files {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte(i*7 + len(f))
			}
			_ = os.WriteFile(filepath.Join(dir, f), b, 0o644)
		}
	}
	seedRule := pathmap.Rule{From: seed, To: "/data/torrents"}
	// Médiathèque : des films rangés sous d'autres noms, et leurs releases V3X
	// (vraies empreintes : la vérification par morceaux passe pour de bon).
	libDir := filepath.Join(media, "Mediatheque")
	_ = os.RemoveAll(libDir)
	t.Cleanup(func() { _ = os.RemoveAll(libDir) })
	for i, x := range []struct{ rel, lib, extra string }{
		{"Panorama.2018.1080p.WEB.H264-DEMO", "Panorama (2018)/Panorama (2018) WEBDL-1080p.mkv", ""},
		{"Horizon.2016.2160p.WEB.H265-DEMO", "Horizon (2016)/Horizon (2016) WEBDL-2160p.mkv", ""},
		{"Lisiere.2021.1080p.WEB.H264-DEMO", "Lisiere (2021)/Lisiere (2021) WEBDL-1080p.mkv", "nfo"},
	} {
		src := filepath.Join(tmp, "v3x-src", x.rel)
		_ = os.MkdirAll(src, 0o755)
		b := make([]byte, 52<<20+i*3_000_001)
		for k := range b {
			b[k] = byte(k*13+i) ^ byte(k>>11)
		}
		_ = os.WriteFile(filepath.Join(src, x.rel+".mkv"), b, 0o644)
		if x.extra != "" {
			_ = os.WriteFile(filepath.Join(src, x.rel+".nfo"), []byte("NFO de démonstration"), 0o644)
		}
		lp := filepath.Join(libDir, filepath.FromSlash(x.lib))
		_ = os.MkdirAll(filepath.Dir(lp), 0o755)
		_ = os.WriteFile(lp, b, 0o644)
		plan, err := maketorrent.Scan(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := plan.Hash(context.Background(), "demo", nil)
		if err != nil {
			t.Fatal(err)
		}
		pt, _ := torrent.Parse(raw)
		id := fmt.Sprintf("0000ab0%d-0000-4000-8000-0000000ab00%d", i, i) // hors des numéros du catalogue
		byID[id] = raw
		libSizes[int64(len(b))] = map[string]interface{}{"id": id, "name": pt.Name, "infoHash": pt.InfoHash, "size": pt.Total, "fileCount": len(pt.Files), "seeders": i, "leechers": 1}
	}
	cfg.Library = []config.LibFolder{{Path: libDir, Source: "local"}}
	cfg.V3XBase, cfg.V3XKey = vs.URL, "demo"
	cfg.Clients = []config.ClientConfig{{ID: "c1", URL: qs.URL, User: "admin", Pass: "demo", PathMap: []pathmap.Rule{seedRule}}}
	if multi {
		cfg.Clients[0].Name = "PC"
		cfg.Clients = append([]config.ClientConfig{{ID: "box", Name: "Seedbox", URL: bs.URL, User: "admin", Pass: "demo"}}, cfg.Clients[0], config.ClientConfig{ID: "nas", Name: "NAS", Kind: "transmission", URL: "http://127.0.0.1:9"})
	}
	// D3X_DEMO_SSH=1 : une seedbox en mémoire (mot de passe « demo »), avec
	// ses propres releases. Il faut les agents compilés (scripts/build-agents.sh).
	if os.Getenv("D3X_DEMO_SSH") != "" {
		box := sshtest.New(t, "demo")
		home := filepath.Join(tmp, "seedbox")
		demoSeedbox(home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("HOME", home)
		cfg.SSH = &config.SSH{Host: box.Host(), Port: box.Port(), User: sshtest.User, HostKey: box.HostLine(), KeyPath: filepath.Join(tmp, "aucune-cle")}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:8799")
	if err != nil {
		t.Fatal(err)
	}
	version := os.Getenv("D3X_DEMO_VERSION")
	if version == "" {
		version = "1.0.0"
	}
	// D3X_DEMO_REMOTE=1 : mode serveur (Docker), pour voir la page d'un NAS.
	opts := Options{}
	if os.Getenv("D3X_DEMO_REMOTE") != "" {
		opts = Options{Remote: true, Managed: "docker"}
	}
	srv := NewWith(version, cfg, "demo", 8799, opts)
	demoAssets(srv.mux)
	go http.Serve(ln, srv)
	t.Logf("démo : http://127.0.0.1:8799/auth?t=demo (%d s)", secs)
	time.Sleep(time.Duration(secs) * time.Second)
}
