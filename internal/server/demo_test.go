package server

// Démo : BARATIE_DEMO=600 go test ./internal/server -run TestDemo -v
// ouvre http://127.0.0.1:8799/auth?t=demo pendant 600 s, avec un faux
// tracker UNIT3D, un faux TMDB, un faux qBittorrent, un faux imgbb et une
// petite médiathèque (fichiers creux, aucune place prise). Sert aussi aux
// captures du README (BARATIE_DEMO_ROOT=/data pour des chemins propres).

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"baratie/internal/config"
	"baratie/internal/qbit"
	"baratie/internal/shots"
	"baratie/internal/tmdb"
	"baratie/internal/torrent"
	"baratie/internal/tracker"
	"baratie/internal/unit3d"
	"baratie/internal/unit3d/fake"
)

type demoFilm struct {
	id                   int
	title, orig, year    string
	lang                 string
	genres               []int
	color1, color2, imdb string
}

var demoFilms = []demoFilm{
	{1001, "Nosferatu", "Nosferatu, eine Symphonie des Grauens", "1922", "de", []int{27}, "#1d2b2a", "#7a9e7e", "tt0013442"},
	{1002, "Metropolis", "Metropolis", "1927", "de", []int{878}, "#2b2118", "#d9a441", "tt0017136"},
	{1003, "Le Mécano de la « General »", "The General", "1926", "en", []int{35}, "#3a2e2a", "#c97b4a", "tt0017925"},
	{1004, "La Nuit des morts-vivants", "Night of the Living Dead", "1968", "en", []int{27}, "#141414", "#b23a3a", "tt0063350"},
	{1005, "La Dame du vendredi", "His Girl Friday", "1940", "en", []int{35}, "#22304a", "#e0c48a", "tt0032599"},
	{1006, "Le Voyage dans la Lune", "Le Voyage dans la Lune", "1902", "fr", []int{878}, "#0f1a3a", "#f2e6a0", "tt0000417"},
	{1007, "Charade", "Charade", "1963", "en", []int{53}, "#3a1f2b", "#f08a5d", "tt0056923"},
	{1008, "Détour", "Detour", "1945", "en", []int{80}, "#1a1a24", "#8fa3c8", "tt0037638"},
	{1009, "Plan 9 from Outer Space", "Plan 9 from Outer Space", "1957", "en", []int{878}, "#101828", "#9be36d", "tt0052077"},
	{1010, "La Petite Boutique des horreurs", "The Little Shop of Horrors", "1960", "en", []int{35}, "#1e3a1e", "#e6e14a", "tt0054033"},
}

func TestDemo(t *testing.T) {
	secs, _ := strconv.Atoi(os.Getenv("BARATIE_DEMO"))
	if secs <= 0 {
		t.Skip("BARATIE_DEMO non défini")
	}
	root := os.Getenv("BARATIE_DEMO_ROOT")
	if root == "" {
		root = filepath.Join(t.TempDir(), "data")
	}
	_ = os.RemoveAll(filepath.Join(root, "downloads"))
	_ = os.RemoveAll(filepath.Join(root, "films"))
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(root, "downloads"))
		_ = os.RemoveAll(filepath.Join(root, "films"))
	})
	confDir := filepath.Join(t.TempDir(), "config")
	if os.Getenv("BARATIE_DEMO_ROOT") != "" {
		confDir = filepath.Join(filepath.Dir(root), "config") // /data → /config, comme dans Docker
		_ = os.RemoveAll(confDir)
	}
	t.Setenv("BARATIE_CONFIG", confDir)
	// Le fichier de règles du tracker, comme chez un vrai membre.
	rules := fake.Rules()
	rules.Name = "MonTracker"
	rules.BannedTeams = map[string]string{"BADTEAM": "team refusée par le règlement"}
	_ = os.MkdirAll(confDir, 0o700)
	if b, err := json.MarshalIndent(rules, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(confDir, "regles.json"), b, 0o600)
	}
	t.Setenv("BARATIE_SEED_DIR", filepath.Join(root, "downloads", "baratie"))
	t.Setenv("BARATIE_MEDIA", root)

	// Affiches : de simples dégradés, servis en https (comme TMDB).
	posters := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(strings.Trim(strings.TrimSuffix(r.URL.Path, ".svg"), "/"))
		for _, f := range demoFilms {
			if f.id == id {
				w.Header().Set("Content-Type", "image/svg+xml")
				fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="185" height="278" viewBox="0 0 185 278"><defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient></defs><rect width="185" height="278" fill="url(#g)"/><text x="92" y="236" font-family="Georgia,serif" font-size="15" fill="#fff" text-anchor="middle">%s</text><text x="92" y="256" font-family="Georgia,serif" font-size="11" fill="#fff" opacity=".8" text-anchor="middle">%s</text></svg>`, f.color1, f.color2, strings.ReplaceAll(f.orig, "&", "&amp;"), f.year)
				return
			}
		}
		w.WriteHeader(404)
	}))
	defer posters.Close()
	tmdb.ImgBase = posters.URL

	// Faux TMDB.
	movie := func(f demoFilm) map[string]interface{} {
		return map[string]interface{}{"id": f.id, "title": f.title, "original_title": f.orig, "release_date": f.year + "-01-01", "imdb_id": f.imdb, "original_language": f.lang, "genre_ids": f.genres, "poster_path": "/" + strconv.Itoa(f.id) + ".svg"}
	}
	tm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/movie":
			q := strings.ToLower(r.URL.Query().Get("query"))
			out := []map[string]interface{}{}
			for _, f := range demoFilms {
				if strings.Contains(strings.ToLower(f.title+" "+f.orig), q) || strings.Contains(q, strings.ToLower(f.orig)) {
					out = append(out, movie(f))
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"results": out})
		case strings.HasPrefix(r.URL.Path, "/movie/"):
			for _, f := range demoFilms {
				if r.URL.Path == "/movie/"+strconv.Itoa(f.id) {
					json.NewEncoder(w).Encode(movie(f))
					return
				}
			}
			w.WriteHeader(404)
		default:
			w.WriteHeader(404)
		}
	}))
	defer tm.Close()
	tmdb.Base = tm.URL
	tmdbFallbackKey = "demo"

	// Médiathèque (fichiers creux) et une vraie release à publier.
	const gib = 1 << 30
	sparse := func(rel string, size int64) {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if fh, err := os.Create(p); err == nil {
			_ = fh.Truncate(size)
			fh.Close()
		}
	}
	sparse("films/Metropolis (1927)/Metropolis.1927.MULTi.1080p.BluRay.x264-DEMO.mkv", 9*gib+11)
	sparse("films/The General (1926)/The.General.1926.MULTi.1080p.BluRay.x264-DEMO.mkv", 7*gib+12)
	sparse("films/Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv", 8*gib+13)
	sparse("films/His Girl Friday (1940)/His.Girl.Friday.1940.MULTi.1080p.WEB.H264-DEMO.mkv", 4*gib+14)
	sparse("films/Charade (1963)/Charade.1963.MULTi.2160p.UHD.BluRay.REMUX.HDR.HEVC-DEMO.mkv", 52*gib+15)
	sparse("films/Detour (1945)/Detour.1945.VOSTFR.1080p.BluRay.x264-DEMO.mkv", 6*gib+16)
	sparse("nanars/Plan.9.From.Outer.Space.1957.FRENCH.720p.HDLight.x264-NANAR.mkv", 2*gib+17)
	sparse("nanars/The.Little.Shop.of.Horrors.1960.MULTi.1080p.WEB.H264-BADTEAM.mkv", 3*gib+18)
	sparse("nanars/Le.Voyage.dans.la.Lune.1902.FRENCH.1080p.BluRay.x264-DEMO.mkv", 2*gib+19)
	rel := filepath.Join(root, "downloads", "Nosferatu.1922.MULTi.1080p.WEB-DL.H264-DEMO")
	_ = os.MkdirAll(rel, 0o755)
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		out := filepath.Join(rel, "Nosferatu.1922.MULTi.1080p.WEB-DL.H264-DEMO.mkv")
		srt := filepath.Join(t.TempDir(), "fr.srt")
		_ = os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:04,000\nSous-titres français\n"), 0o644)
		cmd := exec.Command(ff, "-v", "error", "-y",
			"-f", "lavfi", "-i", "gradients=size=1920x1080:rate=24:duration=40:speed=0.03:c0=0x1d2b2a:c1=0x7a9e7e:c2=0xe8d9b0:c3=0x3b4a6b:nb_colors=4",
			"-f", "lavfi", "-i", "sine=frequency=220:duration=40", "-f", "lavfi", "-i", "sine=frequency=330:duration=40", "-i", srt,
			"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:s",
			"-c:v", "libx264", "-preset", "veryfast", "-b:v", "6M", "-c:a", "ac3", "-b:a", "384k", "-c:s", "srt",
			"-metadata:s:a:0", "language=fre", "-metadata:s:a:0", "title=VFF", "-metadata:s:a:1", "language=ger", "-metadata:s:s:0", "language=fre",
			out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Logf("vidéo de démo impossible : %s", b)
		}
	}
	_ = os.WriteFile(filepath.Join(rel, "Nosferatu.1922.MULTi.1080p.WEB-DL.H264-DEMO.nfo"), []byte("Nosferatu (1922)\nRelease de démonstration.\n"), 0o644)

	// Faux tracker.
	detour, _ := os.Stat(filepath.Join(root, "films/Detour (1945)/Detour.1945.VOSTFR.1080p.BluRay.x264-DEMO.mkv"))
	site := fake.New(
		&fake.Torrent{ID: 4101, Name: "Nosferatu.1922.MULTi.2160p.BluRay.REMUX.HEVC-AUTRE", Size: 61 * gib, Category: 1, Type: "Remux", Resolution: "2160p", TMDB: 1001, Seeders: 14, Files: []int64{61 * gib}},
		&fake.Torrent{ID: 4102, Name: "Nosferatu.1922.VOSTFR.720p.WEB.H264-AUTRE", Size: 2 * gib, Category: 6, Type: "WEB", Resolution: "720p", TMDB: 1001, Seeders: 3, Files: []int64{2 * gib}},
		&fake.Torrent{ID: 4103, Name: "Metropolis.1927.MULTi.1080p.BluRay.x264-AUTRE", Size: 10 * gib, Category: 1, Type: "Encode", Resolution: "1080p", TMDB: 1002, Seeders: 21, Files: []int64{10 * gib}},
		&fake.Torrent{ID: 4104, Name: "The.General.1926.MULTi.720p.WEB.H264-AUTRE", Size: 2 * gib, Category: 1, Type: "WEB", Resolution: "720p", TMDB: 1003, Seeders: 5, Files: []int64{2 * gib}},
		&fake.Torrent{ID: 4105, Name: "His.Girl.Friday.1940.MULTi.1080p.NF.WEB-DL.H264-AUTRE", Size: 5 * gib, Category: 1, Type: "WEB", Resolution: "1080p", TMDB: 1005, Seeders: 8, Files: []int64{5 * gib}},
		&fake.Torrent{ID: 4106, Name: "Detour.1945.VOSTFR.1080p.BluRay.x264-DEMO", Size: detour.Size(), Category: 6, Type: "Encode", Resolution: "1080p", TMDB: 1008, Seeders: 2, Files: []int64{detour.Size()}},
	)
	defer site.Close()
	trackerFor = func(cfg config.Config, ua string, r unit3d.Rules) tracker.Tracker {
		return unit3d.New(site.URL, fake.Token, ua, r)
	}

	// Faux imgbb et faux relais d'images d'UNIT3D.
	ibb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(32 << 20)
		_, h, err := r.FormFile("image")
		if err != nil {
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": 200, "data": map[string]interface{}{
			"url": "https://i.ibb.co/demo/" + h.Filename, "display_url": "https://i.ibb.co/demo/m-" + h.Filename,
			"medium": map[string]string{"url": "https://i.ibb.co/demo/m-" + h.Filename}}})
	}))
	defer ibb.Close()
	shots.ImgBBBase = ibb.URL
	imgbbFallbackKey = "demo"
	wsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG"))
	}))
	defer wsrv.Close()
	shots.WsrvBase = wsrv.URL

	// Faux qBittorrent : ajout à l'arrêt, vérification simulée (2,5 s), départ.
	qs := demoQbit()
	defer qs.Close()

	cfg := config.Defaults()
	cfg.TrackerURL, cfg.TrackerKey = "https://montracker.exemple", "demo"
	cfg.Clients = []config.ClientConfig{{ID: "c1", URL: qs.URL, User: "admin", Pass: "demo"}}
	cfg.PantryLibs = []string{filepath.Join(root, "films"), filepath.Join(root, "nanars")}
	cfg.LastDir = filepath.Join(root, "downloads")
	ln, err := net.Listen("tcp", "127.0.0.1:8799")
	if err != nil {
		t.Fatal(err)
	}
	srv := New("1.0.0", cfg, "demo", 8799, Options{})
	go http.Serve(ln, srv)
	t.Logf("démo : http://127.0.0.1:8799/auth?t=demo (%d s)", secs)
	time.Sleep(time.Duration(secs) * time.Second)
}

// demoQbit : un qBittorrent en mémoire.
func demoQbit() *httptest.Server {
	type entry struct {
		qbit.Torrent
		check time.Time
	}
	var mu sync.Mutex
	list := map[string]*entry{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
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
			for h, e := range list {
				if len(want) > 0 && !want[h] {
					continue
				}
				if !e.check.IsZero() {
					if left := time.Until(e.check); left > 0 {
						e.State, e.Progress = "checkingUP", 1-left.Seconds()/2.5
					} else {
						e.check = time.Time{}
						e.Progress, e.State = 1, "stoppedUP"
					}
				}
				out = append(out, e.Torrent)
			}
			json.NewEncoder(w).Encode(out)
		case "/api/v2/torrents/add":
			_ = r.ParseMultipartForm(1 << 20)
			fh := r.MultipartForm.File["torrents"][0]
			fd, _ := fh.Open()
			raw, _ := io.ReadAll(fd)
			p, _ := torrent.Parse(raw)
			h := strings.ToLower(p.InfoHash)
			save := r.MultipartForm.Value["savepath"][0]
			list[h] = &entry{Torrent: qbit.Torrent{Hash: h, Name: p.Name, SavePath: save, ContentPath: save + "/" + p.Name, State: "stoppedDL", Size: p.Total, TotalSize: p.Total}}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/recheck":
			if e := list[strings.ToLower(r.Form.Get("hashes"))]; e != nil {
				e.check = time.Now().Add(2500 * time.Millisecond)
			}
		case "/api/v2/torrents/start", "/api/v2/torrents/resume":
			if e := list[strings.ToLower(r.Form.Get("hashes"))]; e != nil {
				e.State = "stalledUP"
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}
