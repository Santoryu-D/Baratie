package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/musicbrainz"
)

// Album de démonstration : quatre pistes FLAC taguées (sans édition
// MusicBrainz dans les tags), retrouvées par la recherche grâce à leurs
// durées. Nom sans la qualité : la page propose de l'ajouter.
var demoTracks = []struct {
	title string
	secs  int
}{{"Ouverture", 214}, {"Allegro démo", 187}, {"Andante", 263}, {"Final", 198}}

const (
	demoAlbum   = "Suite pour D3X"
	demoArtist  = "Orchestre Démo"
	demoRelCD   = "d3d3d3d3-0000-4000-8000-000000000001"
	demoRelWeb  = "d3d3d3d3-0000-4000-8000-000000000002"
	demoRelLive = "d3d3d3d3-0000-4000-8000-000000000003"
	demoGroup   = "d3d3d3d3-0000-4000-8000-0000000000aa"
)

func demoFLAC(secs int, kv ...string) []byte {
	rate, bits, ch := 44100, 16, 2
	samples := int64(secs) * int64(rate)
	var b bytes.Buffer
	b.WriteString("fLaC")
	si := make([]byte, 34)
	si[10], si[11] = byte(rate>>12), byte(rate>>4)
	si[12] = byte(rate<<4) | byte((ch-1)<<1) | byte((bits-1)>>4)
	si[13] = byte((bits-1)<<4) | byte(samples>>32&0x0f)
	binary.BigEndian.PutUint32(si[14:18], uint32(samples))
	b.Write([]byte{0x00, 0, 0, 34})
	b.Write(si)
	var c bytes.Buffer
	le := func(n int) { binary.Write(&c, binary.LittleEndian, uint32(n)) }
	le(len("D3X"))
	c.WriteString("D3X")
	le(len(kv))
	for _, s := range kv {
		le(len(s))
		c.WriteString(s)
	}
	b.Write([]byte{0x84, byte(c.Len() >> 16), byte(c.Len() >> 8), byte(c.Len())})
	b.Write(c.Bytes())
	b.Write(make([]byte, 64<<10))
	return b.Bytes()
}

func demoMusicRelease(parent string) {
	dir := filepath.Join(parent, demoArtist+" - "+demoAlbum+" (2024) [FLAC]")
	_ = os.MkdirAll(dir, 0o755)
	for i, t := range demoTracks {
		name := fmt.Sprintf("%02d - %s.flac", i+1, t.title)
		_ = os.WriteFile(filepath.Join(dir, name), demoFLAC(t.secs, "ARTIST="+demoArtist, "ALBUMARTIST="+demoArtist, "ALBUM="+demoAlbum, "DATE=2024", "TITLE="+t.title, fmt.Sprintf("TRACKNUMBER=%d", i+1), "MEDIA=Digital Media", "LABEL=Disques Démo"), 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte{0xff, 0xd8, 0xff}, 0o644)
}

// demoMusicBrainz : trois éditions, dont deux aux mêmes pistes (CD et
// numérique) et un enregistrement en public aux durées différentes.
func demoMusicBrainz(t *testing.T) {
	edition := func(id, date, format string, shift int) map[string]interface{} {
		var tracks []interface{}
		for i, tr := range demoTracks {
			tracks = append(tracks, map[string]interface{}{"position": i + 1, "title": tr.title, "length": (tr.secs + shift) * 1000})
		}
		return map[string]interface{}{
			"id": id, "title": demoAlbum, "date": date, "country": "FR", "status": "Official", "score": 100,
			"artist-credit": []interface{}{map[string]interface{}{"name": demoArtist}},
			"release-group": map[string]interface{}{"id": demoGroup, "primary-type": "Album"},
			"label-info":    []interface{}{map[string]interface{}{"catalog-number": "DEMO-001", "label": map[string]interface{}{"name": "Disques Démo"}}},
			"media":         []interface{}{map[string]interface{}{"format": format, "position": 1, "track-count": len(demoTracks), "tracks": tracks}},
		}
	}
	rel := map[string]map[string]interface{}{
		demoRelCD:   edition(demoRelCD, "2024-03-01", "CD", 0),
		demoRelWeb:  edition(demoRelWeb, "2024-03-01", "Digital Media", 1),
		demoRelLive: edition(demoRelLive, "2025-06-12", "Digital Media", 40),
	}
	rel[demoRelLive]["title"] = demoAlbum + " (en public)"
	rel[demoRelLive]["release-group"] = map[string]interface{}{"id": "d3d3d3d3-0000-4000-8000-0000000000bb", "primary-type": "Album"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/release" {
			var list []interface{}
			if strings.Contains(strings.ToLower(r.URL.Query().Get("query")), "suite") {
				for _, id := range []string{demoRelCD, demoRelWeb, demoRelLive} {
					e := rel[id]
					m := e["media"].([]interface{})[0].(map[string]interface{})
					cp := map[string]interface{}{}
					for k, v := range e {
						cp[k] = v
					}
					cp["media"] = []interface{}{map[string]interface{}{"format": m["format"], "track-count": m["track-count"]}}
					cp["track-count"] = m["track-count"]
					list = append(list, cp)
				}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"releases": list})
			return
		}
		if e := rel[strings.TrimPrefix(r.URL.Path, "/release/")]; e != nil {
			json.NewEncoder(w).Encode(e)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)
	musicbrainz.Base = ts.URL
}

// demoMusicFiche : la fiche album que renverrait V3X pour une édition.
func demoMusicFiche(releaseName string, mbRef *string) map[string]interface{} {
	warnings := []string{}
	if !strings.Contains(strings.ToLower(releaseName), "bit") {
		warnings = append(warnings, "audio_naming")
	}
	out := map[string]interface{}{
		"analysis":      map[string]interface{}{"title": demoAlbum, "tokens": []map[string]string{{"kind": "year", "value": "2024"}}, "warnings": []string{}},
		"language":      "",
		"season":        nil,
		"suggestedName": releaseName,
		"title":         releaseName,
		"tmdbQuery":     "",
		"category":      map[string]interface{}{"id": 41, "slug": "audio-musique"},
		"teamRelease":   false,
		"nfo":           "Release de démonstration",
		"presentation":  "[center][size=5][b]" + demoAlbum + "[/b][/size]\n[i]" + demoArtist + "[/i][/center]",
		"warnings":      warnings,
		"music":         nil,
		"creator":       nil,
	}
	if mbRef != nil {
		out["presentation"] = demoAlbumFiche()
		out["title"] = demoArtist + " - " + demoAlbum + " (2024)"
		out["music"] = map[string]interface{}{"title": out["title"], "artist": demoArtist, "album": demoAlbum, "year": "2024", "trackCount": len(demoTracks)}
		out["creator"] = demoArtist
	}
	return out
}
