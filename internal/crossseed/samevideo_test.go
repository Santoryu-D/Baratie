package crossseed

import (
	"context"
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

// Image disque d'un jeu (Nayr, 22/10/26 : Ori.and.the.Will.of.the.Wisps-Mephisto
// affiché « Pas sur V3X », un .nfo différent à côté de l'.iso) : « Même fichier
// sur V3X » seulement si la fiche porte le même nom de release ; un autre jeu
// de la même taille reste « Pas sur V3X ».
func TestSameFileISO(t *testing.T) {
	const oriISO = "Ori.and.the.Will.of.the.Wisps-Mephisto.iso"
	for _, tc := range []struct {
		name, local, v3xName, v3xFile, want string
	}{
		{"même nom", "Ori.and.the.Will.of.the.Wisps-Mephisto", "Ori.and.the.Will.of.the.Wisps-Mephisto", "", StSameVideo},
		{"autre jeu", "Ori.and.the.Will.of.the.Wisps-Mephisto", "Un.Autre.Jeu-GRP", "Un.Autre.Jeu-GRP.iso", StNone},
		// Nayr, 22/10/26 : la copie vient d'ailleurs sous un autre nom de
		// release, l'image a le même nom de fichier que celle de la fiche.
		{"autre nom, même fichier", "Ori.and.the.Will.of.the.Wisps.MULTi.ISO-Mephisto", "Ori.and.the.Will.of.the.Wisps-Mephisto", oriISO, StSameVideo},
		{"autre nom, autre fichier", "Ori.and.the.Will.of.the.Wisps.MULTi.ISO-Mephisto", "Ori.and.the.Will.of.the.Wisps-Mephisto", "setup.iso", StNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedConfig(t)
			const iso = int64(400_000_000)
			raw := mkTorrent(tc.local, map[string]int64{
				tc.local + "/" + oriISO:    iso,
				tc.local + "/mephisto.nfo": 5_000,
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
			vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/me":
					io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
				case "/api/d3x/match":
					io.WriteString(w, `{"results":[]}`)
				case "/api/d3x/match-files":
					json.NewEncoder(w).Encode(map[string]interface{}{"matches": map[string][]v3x.FileMatch{
						"400000000": {{ID: "66666666-6666-4666-8666-666666666666", Name: tc.v3xName, File: tc.v3xFile, InfoHash: strings.Repeat("cd", 20), Size: iso + 8_774, FileCount: 2, Seeders: 4}},
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
			for _, r := range e.Rows() {
				if r.Name != p.Name {
					continue
				}
				if r.Status != tc.want {
					t.Fatalf("%s : %s (%s), attendu %s", tc.name, r.Status, r.Detail, tc.want)
				}
				if tc.want == StSameVideo && (r.Match == nil || r.Match.Kind != "same_file" || !strings.Contains(r.Detail, ".iso")) {
					t.Fatalf("correspondance : %+v (%s)", r.Match, r.Detail)
				}
				if tc.want == StNone && r.Match != nil {
					t.Fatalf("autre jeu rattaché : %+v", r.Match)
				}
				// Diagnostic (1.1.28) : les fichiers et leurs tailles, jamais l'info-hash.
				d, ok := e.Diag(context.Background(), r.Key, "test")
				if !ok || !strings.Contains(d, oriISO+" : 400000000 octets") || strings.Contains(d, h) {
					t.Fatalf("diagnostic : %s", d)
				}
				return
			}
			t.Fatalf("ligne absente : %+v", e.Rows())
		})
	}
}

func TestSameReleaseName(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Ori.and.the.Will.of.the.Wisps-Mephisto", "Ori and the Will of the Wisps-Mephisto", true},
		{"Ori.and.the.Will.of.the.Wisps-Mephisto.iso", "ori.and.the.will.of.the.wisps-mephisto", true},
		{"Ori.and.the.Will.of.the.Wisps-Mephisto", "Ori.and.the.Blind.Forest-Mephisto", false},
		{"...", "---", false},
	} {
		if got := sameReleaseName(tc.a, tc.b); got != tc.want {
			t.Errorf("%q / %q : %v, attendu %v", tc.a, tc.b, got, tc.want)
		}
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

// Ori de Nayr, 23/10/26 : un .iso seul rangé dans un dossier, et une empreinte
// en cache d'une version d'avant (sans le plus gros fichier). D3X prenait le
// nom du dossier pour celui du fichier (extension « .wisps-mephisto ») et ne
// rapprochait rien ; le .torrent se relit et l'image disque est reconnue.
func TestSameFileISOAloneInFolder(t *testing.T) {
	isolatedConfig(t)
	const name = "Ori.and.the.Will.of.the.Wisps-Mephisto"
	const iso = int64(400_000_000)
	raw := mkTorrent(name, map[string]int64{name + "/" + name + ".iso": iso}, "https://autre.invalid/announce", "AUTRE")
	p, _ := torrent.Parse(raw)
	h := strings.ToLower(p.InfoHash)
	// Empreinte d'une version d'avant : ni Big, ni BigExt, ni BigName.
	saveSigCache(map[string]Sig{h: {InfoHash: h, Name: p.Name, ContentHash: p.ContentHash, SizeHash: p.SizeHash, Total: p.Total, Real: p.Total, Files: 1}})
	fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}}
	fq.list[h] = &fakeTorrent{Torrent: qbit.Torrent{
		Hash: h, Name: p.Name, SavePath: "/data", ContentPath: "/data/" + p.Name, Progress: 1, State: "stalledUP",
		Size: p.Total, TotalSize: p.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2Fautre.invalid%2Fannounce",
	}, raw: raw}
	qs := httptest.NewServer(fq.handler(t))
	defer qs.Close()
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
		case "/api/d3x/match":
			io.WriteString(w, `{"results":[]}`)
		case "/api/d3x/match-files":
			json.NewEncoder(w).Encode(map[string]interface{}{"matches": map[string][]v3x.FileMatch{
				"400000000": {{ID: "66666666-6666-4666-8666-666666666666", Name: name, File: name + ".iso", InfoHash: strings.Repeat("cd", 20), Size: iso + 8_774, FileCount: 2, Seeders: 4}},
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
	for _, r := range e.Rows() {
		if r.Name != name {
			continue
		}
		if r.Status != StSameVideo || r.Match == nil || r.Match.Kind != "same_file" {
			t.Fatalf("%s (%s) %+v, attendu Même fichier sur V3X", r.Status, r.Detail, r.Match)
		}
		return
	}
	t.Fatalf("ligne absente : %+v", e.Rows())
}

func TestLooksLikeFileExt(t *testing.T) {
	for ext, want := range map[string]bool{".mkv": true, ".iso": true, ".epub": true, ".wisps-mephisto": false, ".x264": false, ".h265-grp": false, ".1080p": false} {
		if got := looksLikeFileExt(ext); got != want {
			t.Errorf("%s : %v, attendu %v", ext, got, want)
		}
	}
}

// Livre de Luci, 23/10/26 : un .epub dont le nom ne diffère de la fiche que par
// les majuscules. Rapproché par les tailles seules, il n'est gardé qu'au même
// nom à la casse près ; un autre livre de même taille reste « Pas sur V3X ».
func TestSmallRenamedNameCheck(t *testing.T) {
	for _, tc := range []struct{ v3xName, want string }{
		{"Charles Bukowski - Contes de la folie ordinaire.epub", StRenamed},
		{"Un Autre Livre.epub", StNone},
	} {
		t.Run(tc.want, func(t *testing.T) {
			isolatedConfig(t)
			const local = "Charles Bukowski - Contes de la Folie Ordinaire.epub"
			raw := mkTorrent(local, map[string]int64{local: 212_666}, "https://autre.invalid/announce", "AUTRE")
			p, _ := torrent.Parse(raw)
			h := strings.ToLower(p.InfoHash)
			fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{}}
			fq.list[h] = &fakeTorrent{Torrent: qbit.Torrent{
				Hash: h, Name: p.Name, SavePath: "/data", ContentPath: "/data/" + p.Name, Progress: 1, State: "stalledUP",
				Size: p.Total, TotalSize: p.Total, MagnetURI: "magnet:?xt=urn:btih:" + h + "&tr=https%3A%2F%2Fautre.invalid%2Fannounce",
			}, raw: raw}
			qs := httptest.NewServer(fq.handler(t))
			defer qs.Close()
			asked := false
			vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/me":
					io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
				case "/api/d3x/match":
					var in struct {
						Items      []struct{ Key string }
						SmallNames bool
					}
					json.NewDecoder(r.Body).Decode(&in)
					asked = in.SmallNames
					json.NewEncoder(w).Encode(map[string]interface{}{"results": []map[string]interface{}{{"key": in.Items[0].Key, "match": map[string]interface{}{
						"kind": "renamed", "id": "77777777-7777-4777-8777-777777777777", "name": tc.v3xName, "infoHash": strings.Repeat("ef", 20), "seeders": 8, "size": 212_666, "nameCheck": true,
					}}}})
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
			if !asked {
				t.Fatal("smallNames non demandé au site")
			}
			for _, r := range e.Rows() {
				if r.Name != local {
					continue
				}
				if r.Status != tc.want {
					t.Fatalf("%s (%s), attendu %s", r.Status, r.Detail, tc.want)
				}
				return
			}
			t.Fatalf("ligne absente : %+v", e.Rows())
		})
	}
}
