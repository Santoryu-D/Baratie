package torrent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/bencode"
)

type fixFile struct {
	path []string
	size int64
	attr string
}

type fixture struct {
	name  string // nom du fichier de test
	tname []byte // info.name (octets bruts : UTF-8 invalide possible)
	files []fixFile
	size  int64 // mono-fichier si files == nil
	// Valeurs calculées par le site (lib/torrent-parse.ts, Node) sur ces
	// mêmes octets : D3X doit retomber exactement dessus.
	wantContent string
	wantSize    string
}

func build(f fixture) []byte {
	const pl = int64(1 << 20)
	info := bencode.Dict{"name": f.tname, "piece length": pl}
	var total int64
	if f.files == nil {
		info["length"] = f.size
		total = f.size
	} else {
		var list []bencode.Value
		for _, x := range f.files {
			parts := make([]bencode.Value, len(x.path))
			for i, p := range x.path {
				parts[i] = []byte(p)
			}
			d := bencode.Dict{"length": x.size, "path": parts}
			if x.attr != "" {
				d["attr"] = []byte(x.attr)
			}
			list = append(list, d)
			total += x.size
		}
		info["files"] = list
	}
	n := (total + pl - 1) / pl
	info["pieces"] = make([]byte, n*20)
	b, err := bencode.Encode(bencode.Dict{"announce": []byte("http://tracker.invalid/announce"), "info": info})
	if err != nil {
		panic(err)
	}
	return b
}

var fixtures = []fixture{
	{
		name: "mono", tname: []byte("Film.2020.MULTi.1080p.WEB.H264-TEAM.mkv"), size: 4_831_838_208,
		wantContent: "bc81d2f58bb6e6d2681ac807cc5fafd7307f4a8f4e2aa1c8b5f6763169d4642a",
		wantSize:    "7a1b4a96103bd07d9a29e1c27b6730ab8c48dd2abb70a143126e79646088cd07",
	},
	{
		name: "accents", tname: []byte("Série Été 2024 🎬 S01"),
		files: []fixFile{
			{path: []string{"Épisode 01 – Le début.mkv"}, size: 1_500_000_123},
			{path: []string{"Sous-titres", "fr.srt"}, size: 45_210},
			{path: []string{".pad", "1048000"}, size: 1_048_000, attr: "p"},
			{path: []string{"Épisode 02.mkv"}, size: 1_400_000_777},
			{path: []string{"bourrage"}, size: 12_345, attr: "p"},
			{path: []string{"zz", ".pad", "3"}, size: 3},
		},
		wantContent: "a8948cd6ac9766a4fedf4f880c5bc284aac95f0008ef4935ef6424e1a14bc5eb",
		wantSize:    "fd3d05c3e3bba5e712e6d4ab98881b48191a0fdd942e84cfd439175bb5cd8dc7",
	},
	{
		name: "latin1", tname: []byte("Am\xe9lie \xe9\xa9s \xe2\x82 fin\xf0\x9f"),
		files: []fixFile{
			{path: []string{"Am\xe9lie.avi"}, size: 700_000_000},
			{path: []string{"\xed\xa0\x80.txt"}, size: 10},
			{path: []string{"ok.nfo"}, size: 2_000},
		},
		wantContent: "00043c235cf0814d52ec1b8e7f390f6b7bbf4db088ddba830f419a655f6de559",
		wantSize:    "8c9b5da1c343b03846f0cefa26fbdec323730e1e4466ba1c0d0d57bc995976d2",
	},
	{
		name: "tri", tname: []byte("Tri"),
		files: []fixFile{
			{path: []string{"！.txt"}, size: 1},
			{path: []string{"😀.txt"}, size: 2},
			{path: []string{"a b.txt"}, size: 3},
			{path: []string{"a", "b.txt"}, size: 4},
			{path: []string{"A.txt"}, size: 5},
			{path: []string{"é.txt"}, size: 6},
		},
		wantContent: "dcbec514ce576a33c634863f34b3e9ebf8ee2dace70ea505ace65e6e675e8d1b",
		wantSize:    "1313c7e39dab61592d7a2061d62d11d2db116f43f993394098eeed7a5f8184b6",
	},
	{
		name: "long", tname: []byte("Long"),
		files: []fixFile{
			{path: []string{strings.Repeat("d", 600), strings.Repeat("f", 600) + ".mkv"}, size: 123_456_789},
			{path: []string{"court.mkv"}, size: 987_654_321},
		},
		wantContent: "c5f2ca3e168d2d1f2c90490ffdcde42126305e18cc8172b18e8bd53c7a1fa818",
		wantSize:    "d0fece4db885709f0593963dcf3595c0a29144600e1bdf156f91ca2291fbed37",
	},
}

// D3X_FIXTURES=dossier : écrit les .torrent de test et ce que D3X en tire,
// pour comparer au calcul du site.
func TestDumpFixtures(t *testing.T) {
	dir := os.Getenv("D3X_FIXTURES")
	if dir == "" {
		t.Skip("D3X_FIXTURES non défini")
	}
	out := map[string][2]string{}
	for _, f := range fixtures {
		raw := build(f)
		if err := os.WriteFile(filepath.Join(dir, f.name+".torrent"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		p, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s : %v", f.name, err)
		}
		out[f.name] = [2]string{p.ContentHash, p.SizeHash}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "go.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSignaturesMatchSite(t *testing.T) {
	for _, f := range fixtures {
		if f.wantContent == "" {
			continue
		}
		p, err := Parse(build(f))
		if err != nil {
			t.Fatalf("%s : %v", f.name, err)
		}
		if p.ContentHash != f.wantContent {
			t.Errorf("%s : contentHash %s, le site calcule %s", f.name, p.ContentHash, f.wantContent)
		}
		if p.SizeHash != f.wantSize {
			t.Errorf("%s : sizeHash %s, le site calcule %s", f.name, p.SizeHash, f.wantSize)
		}
	}
}

func TestPadFilesExcluded(t *testing.T) {
	p, err := Parse(build(fixtures[1]))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 3 {
		t.Fatalf("%d fichiers, 3 attendus (bourrage exclu)", len(p.Files))
	}
	var sum int64
	for _, f := range fixtures[1].files {
		sum += f.size
	}
	if p.Total != sum {
		t.Fatalf("total %d, %d attendu (bourrage compris)", p.Total, sum)
	}
}

func TestNodeString(t *testing.T) {
	cases := map[string]string{
		"abc":               "abc",
		"Am\xe9lie":         "Am�lie",
		"\xe9\xa9s":         "�s", // sous-partie maximale E9 A9 : UN remplacement
		"\xe2\x82":          "�",
		"\xf0\x9f":          "�",
		"\xed\xa0\x80":      "���", // ED n'accepte que 80..9F
		"\xc0\xaf":          "��",
		"ok\xffok":          "ok�ok",
		"\xf0\x9f\x98\x80":  "😀",
		"\xf4\x90\x80\x80x": "����x",
	}
	for in, want := range cases {
		if got := nodeString([]byte(in)); got != want {
			t.Errorf("nodeString(%q) = %q, attendu %q", in, got, want)
		}
	}
}
