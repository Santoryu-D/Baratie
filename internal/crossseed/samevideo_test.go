package crossseed

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/qbit"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// Même vidéo sur V3X sous une autre forme (Motives chez Luci, 21/10/26) : la
// copie est un .mkv seul au nom d'un autre site, la fiche V3X un dossier avec
// la vidéo sous son nom d'origine et un .nfo. Ni « Pas sur V3X », ni ajout :
// « Même vidéo sur V3X », ou « Déjà dans le client » si la fiche V3X y est.
func TestSameVideo(t *testing.T) {
	for _, inClient := range []bool{false, true} {
		t.Run(map[bool]string{false: "ailleurs", true: "dans le client"}[inClient], func(t *testing.T) {
			isolatedConfig(t)
			const video = int64(300_000_000)
			other := mkTorrent("Film.2004.MULTi.VFF.1080p-FW", map[string]int64{"Film.2004.MULTi.VFF.1080p-FW.mkv": video}, "https://autre.invalid/announce", "AUTRE")
			onV3X := mkTorrent("Film.2004.MULTi.1080p-FW", map[string]int64{"Film.2004.MULTi.1080p-FW/Film.2004.MULTi.1080p-FW.mkv": video, "Film.2004.MULTi.1080p-FW/Film.2004.MULTi.1080p-FW.nfo": 7393}, "https://announce.v3x.club/pk/announce", "")
			po, _ := torrent.Parse(other)
			pv, _ := torrent.Parse(onV3X)

			fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}}
			h := strings.ToLower(po.InfoHash)
			fq.list[h] = &fakeTorrent{Torrent: qbit.Torrent{
				Hash: h, Name: po.Name, SavePath: "/data", ContentPath: "/data/" + po.Name, Progress: 1, State: "stalledUP",
				Size: po.Total, TotalSize: po.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2Fautre.invalid%2Fannounce",
			}, raw: other}
			if inClient {
				hv := strings.ToLower(pv.InfoHash)
				fq.list[hv] = &fakeTorrent{Torrent: qbit.Torrent{
					Hash: hv, Name: pv.Name, SavePath: "/data", ContentPath: "/data/" + pv.Name, Progress: 1, State: "stalledUP",
					Size: pv.Total, TotalSize: pv.Total, MagnetURI: "magnet:?xt=urn:btih:" + hv + "&tr=https%3A%2F%2Fannounce.v3x.club%2Fpk%2Fannounce",
				}, raw: onV3X}
			}
			qs := httptest.NewServer(fq.handler(t))
			defer qs.Close()

			var asked []int64
			vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/me":
					io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
				case "/api/d3x/match":
					io.WriteString(w, `{"results":[]}`)
				case "/api/d3x/match-files":
					var in struct{ Sizes []int64 }
					json.NewDecoder(r.Body).Decode(&in)
					asked = in.Sizes
					json.NewEncoder(w).Encode(map[string]interface{}{"matches": map[string][]v3x.FileMatch{
						"300000000": {{ID: "44444444-4444-4444-4444-444444444444", Name: pv.Name, InfoHash: pv.InfoHash, Size: pv.Total, FileCount: 2, Seeders: 4}},
					}})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer vs.Close()

			q, _ := client.New(client.Options{URL: qs.URL, User: "admin", Pass: "secret"})
			e := New(oneClient(q, v3x.New(vs.URL, "cle", "D3X/test"), config.Defaults()))
			if err := e.StartScan(); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return !e.Scanning() })
			if len(asked) != 1 || asked[0] != video {
				t.Fatalf("tailles demandées au site : %v", asked)
			}
			want := StSameVideo
			if inClient {
				want = StInClient
			}
			for _, r := range e.Rows() {
				if r.Name != po.Name {
					continue
				}
				if r.Status != want || r.Match == nil || r.Match.Kind != "same_video" || !strings.Contains(r.Detail, pv.Name) {
					t.Fatalf("copie de l'autre site : %s (%s), attendu %s", r.Status, r.Detail, want)
				}
				return
			}
			t.Fatalf("ligne absente : %+v", e.Rows())
		})
	}
}

// Un installeur de jeu découpé en morceaux de taille fixe (Nayr, 22/10/26 :
// un Terminator pris pour Baldur's Gate 3) : son plus gros fichier n'est pas une
// vidéo, sa taille ne part pas au site et la ligne reste « Pas sur V3X ».
func TestSameVideoIgnoresGameParts(t *testing.T) {
	isolatedConfig(t)
	const part = int64(300_000_000)
	raw := mkTorrent("terminator_win_gog", map[string]int64{
		"terminator_win_gog/setup_terminator.exe":   5_000_000,
		"terminator_win_gog/setup_terminator-1.bin": part,
		"terminator_win_gog/setup_terminator-2.bin": part - 1000,
	}, "https://autre.invalid/announce", "AUTRE")
	p, _ := torrent.Parse(raw)
	fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}}
	h := strings.ToLower(p.InfoHash)
	fq.list[h] = &fakeTorrent{Torrent: qbit.Torrent{
		Hash: h, Name: p.Name, SavePath: "/data", ContentPath: "/data/" + p.Name, Progress: 1, State: "stalledUP",
		Size: p.Total, TotalSize: p.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2Fautre.invalid%2Fannounce",
	}, raw: raw}
	qs := httptest.NewServer(fq.handler(t))
	defer qs.Close()
	var asked []int64
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
		case "/api/d3x/match":
			io.WriteString(w, `{"results":[]}`)
		case "/api/d3x/match-files":
			var in struct{ Sizes []int64 }
			json.NewDecoder(r.Body).Decode(&in)
			asked = append(asked, in.Sizes...)
			json.NewEncoder(w).Encode(map[string]interface{}{"matches": map[string][]v3x.FileMatch{
				"300000000": {{ID: "55555555-5555-4555-8555-555555555555", Name: "baldurs.gate.3.GOG", InfoHash: strings.Repeat("ab", 20), Size: 135 << 30, FileCount: 40, Seeders: 4}},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()
	q, _ := client.New(client.Options{URL: qs.URL, User: "admin", Pass: "secret"})
	e := New(oneClient(q, v3x.New(vs.URL, "cle", "D3X/test"), config.Defaults()))
	if err := e.StartScan(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !e.Scanning() })
	if len(asked) != 0 {
		t.Fatalf("tailles d'un installeur envoyées au site : %v", asked)
	}
	for _, r := range e.Rows() {
		if r.Name == p.Name {
			if r.Status != StNone || r.Match != nil {
				t.Fatalf("installeur de jeu : %s (%s), attendu %s", r.Status, r.Detail, StNone)
			}
			return
		}
	}
	t.Fatalf("ligne absente : %+v", e.Rows())
}

// Une release seedée sur plusieurs trackers (un torrent chacun, sur des liens
// physiques ailleurs) : une seule ligne « Pas sur V3X », copies comptées
// (exodius19, 21/10/26 : quatre fois le même Astérix).
func TestNoneCopiesMerged(t *testing.T) {
	isolatedConfig(t)
	files := map[string]int64{"Film.1968.FRENCH.1080p-GRP.mkv": 1_200_000_000}
	fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}}
	var name string
	for _, src := range []string{"UN", "DEUX", "TROIS"} {
		raw := mkTorrent("Film.1968.FRENCH.1080p-GRP", files, "https://autre.invalid/announce", src)
		p, _ := torrent.Parse(raw)
		h := strings.ToLower(p.InfoHash)
		name = p.Name
		dir := "/data/" + src
		fq.list[h] = &fakeTorrent{Torrent: qbit.Torrent{
			Hash: h, Name: p.Name, SavePath: dir, ContentPath: dir + "/" + p.Name, Progress: 1, State: "stalledUP",
			Size: p.Total, TotalSize: p.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2Fautre.invalid%2Fannounce",
		}, raw: raw}
	}
	qs := httptest.NewServer(fq.handler(t))
	defer qs.Close()
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
		case "/api/d3x/match":
			io.WriteString(w, `{"results":[]}`)
		case "/api/d3x/match-files":
			io.WriteString(w, `{"matches":{}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer vs.Close()
	q, _ := client.New(client.Options{URL: qs.URL, User: "admin", Pass: "secret"})
	e := New(oneClient(q, v3x.New(vs.URL, "cle", "D3X/test"), config.Defaults()))
	if err := e.StartScan(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !e.Scanning() })
	var got []Row
	for _, r := range e.Rows() {
		if r.Name == name {
			got = append(got, r)
		}
	}
	if len(got) != 1 || got[0].Status != StNone || got[0].Copies != 3 {
		t.Fatalf("attendu une ligne « Pas sur V3X » à 3 copies : %+v", got)
	}
}
