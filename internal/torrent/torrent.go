// Package torrent lit un .torrent et calcule les mêmes empreintes que le tracker
// (apps/api/src/lib/torrent-parse.ts côté site) : c'est ce qui permet de savoir,
// sans rien envoyer d'autre, si une release de ton client existe déjà sur le tracker.
package torrent

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"baratie/internal/bencode"
)

// File est un fichier du torrent, chemin complet avec le dossier racine.
type File struct {
	Path   string
	Size   int64
	Index  int   // rang dans la liste du .torrent, fichiers de bourrage compris
	Offset int64 // position dans la suite des données (bourrage compris)
}

// Torrent est un .torrent lu.
type Torrent struct {
	InfoHash    string // sha1 hex des octets bruts du dict « info »
	Name        string
	PieceLength int64
	Files       []File // hors fichiers de bourrage (BEP 47)
	Total       int64  // taille totale déclarée (bourrage compris, comme le tracker)
	ContentHash string // même calcul que contentSignature côté site
	SizeHash    string // même calcul que sizeSignature côté site
	RawInfo     []byte
	FileCount   int    // fichiers du .torrent, bourrage compris
	Pieces      []byte // empreintes SHA-1 des morceaux, 20 octets chacune
}

var padDir = regexp.MustCompile(`(^|/)\.pad/`)

// IsPadPath dit si un chemin (avec ou sans la racine) est un fichier de
// bourrage (BEP 47, dossier « .pad/ »).
func IsPadPath(p string) bool { return padDir.MatchString(p) }

// ErrInvalid signale un .torrent illisible.
var ErrInvalid = errors.New("torrent invalide")

// Parse lit un fichier .torrent.
func Parse(data []byte) (*Torrent, error) {
	d, err := bencode.Decode(data)
	if err != nil {
		return nil, err
	}
	root, ok := d.Root.(bencode.Dict)
	if !ok || d.RawInfo == nil {
		return nil, ErrInvalid
	}
	info, ok := root["info"].(bencode.Dict)
	if !ok {
		return nil, ErrInvalid
	}
	nameB, ok := info["name"].([]byte)
	if !ok {
		return nil, ErrInvalid
	}
	name := truncUTF16(nodeString(nameB), 255)
	pl, _ := info["piece length"].(int64)

	t := &Torrent{Name: name, PieceLength: pl, RawInfo: d.RawInfo}
	if p, ok := info["pieces"].([]byte); ok && len(p)%20 == 0 {
		t.Pieces = p
	}
	sum := sha1.Sum(d.RawInfo)
	t.InfoHash = hex.EncodeToString(sum[:])

	if l, ok := info["length"].(int64); ok {
		t.Total = l
		t.Files = []File{{Path: name, Size: l}}
		t.FileCount = 1
	} else {
		list, ok := info["files"].([]bencode.Value)
		if !ok {
			return nil, ErrInvalid
		}
		t.FileCount = len(list)
		for i, e := range list {
			f, ok := e.(bencode.Dict)
			if !ok {
				return nil, ErrInvalid
			}
			size, _ := f["length"].(int64)
			parts, ok := f["path"].([]bencode.Value)
			if !ok {
				return nil, ErrInvalid
			}
			segs := make([]string, 0, len(parts))
			for _, p := range parts {
				b, _ := p.([]byte)
				segs = append(segs, nodeString(b))
			}
			path := truncUTF16(strings.Join(segs, "/"), 1024)
			offset := t.Total
			t.Total += size
			attr := ""
			if a, ok := f["attr"].([]byte); ok {
				attr = nodeString(a)
			}
			if strings.Contains(attr, "p") || padDir.MatchString(path) {
				continue
			}
			t.Files = append(t.Files, File{Path: name + "/" + path, Size: size, Index: i, Offset: offset})
		}
	}
	if t.Total <= 0 {
		return nil, ErrInvalid
	}
	t.ContentHash = ContentSignature(t.Files, t.Total)
	t.SizeHash = SizeSignature(t.Files, t.Total)
	return t, nil
}

// InnerPieces : jusqu'à max morceaux entièrement contenus dans f, répartis
// du début à la fin du fichier : ceux qu'on vérifie avec ce seul fichier.
func (t *Torrent) InnerPieces(f File, max int) []int {
	if t.PieceLength <= 0 || max <= 0 {
		return nil
	}
	first := int((f.Offset + t.PieceLength - 1) / t.PieceLength)
	last := int((f.Offset+f.Size)/t.PieceLength) - 1 // dernier morceau complet
	if n := len(t.Pieces) / 20; last >= n {
		last = n - 1
	}
	if last < first {
		return nil
	}
	n := last - first + 1
	if n <= max {
		out := make([]int, 0, n)
		for i := first; i <= last; i++ {
			out = append(out, i)
		}
		return out
	}
	if max == 1 {
		return []int{first + n/2}
	}
	out := make([]int, 0, max)
	for k := 0; k < max; k++ {
		out = append(out, first+k*(n-1)/(max-1))
	}
	return out
}

// PieceHash : l'empreinte SHA-1 attendue du morceau i.
func (t *Torrent) PieceHash(i int) []byte {
	if i < 0 || (i+1)*20 > len(t.Pieces) {
		return nil
	}
	return t.Pieces[i*20 : (i+1)*20]
}

// ContentSignature : « chemin\ntaille » de chaque fichier, triés comme le fait
// JavaScript (ordre des unités UTF-16), joints par \n, puis « \n::total::N ».
// Les chemins commencent par le nom du torrent (mono-fichier : le nom seul).
func ContentSignature(files []File, total int64) string {
	lines := make([]string, len(files))
	for i, f := range files {
		lines[i] = f.Path + "\n" + strconv.FormatInt(f.Size, 10)
	}
	sort.Slice(lines, func(i, j int) bool { return lessUTF16(lines[i], lines[j]) })
	return sha256Hex(strings.Join(lines, "\n") + "\n::total::" + strconv.FormatInt(total, 10))
}

// SizeSignature : tailles triées par valeur, jointes par \n, puis le total.
func SizeSignature(files []File, total int64) string {
	sizes := make([]int64, len(files))
	for i, f := range files {
		sizes[i] = f.Size
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
	parts := make([]string, len(sizes))
	for i, s := range sizes {
		parts[i] = strconv.FormatInt(s, 10)
	}
	return sha256Hex(strings.Join(parts, "\n") + "\n::total::" + strconv.FormatInt(total, 10))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// nodeString reproduit Buffer.toString("utf8") de Node, qui suit le décodeur
// UTF-8 de la norme WHATWG : chaque « sous-partie maximale » invalide devient
// UN seul U+FFFD. Go en mettrait un par octet, et un vieux nom en Latin-1
// (« é© » = E9 A9) donnerait une autre empreinte que celle du site.
func nodeString(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var (
		sb           strings.Builder
		cp           rune
		needed, seen int
		lower, upper byte = 0x80, 0xBF
	)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c <= 0x7F:
				sb.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				} else if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				} else if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				sb.WriteRune(utf8.RuneError)
			}
			continue
		}
		if c < lower || c > upper {
			// Séquence interrompue : un seul remplacement, et l'octet est relu.
			cp, needed, seen, lower, upper = 0, 0, 0, 0x80, 0xBF
			sb.WriteRune(utf8.RuneError)
			i--
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			sb.WriteRune(cp)
			cp, needed, seen = 0, 0, 0
		}
	}
	if needed != 0 {
		sb.WriteRune(utf8.RuneError)
	}
	return sb.String()
}

// truncUTF16 reproduit String.prototype.slice(0, n) (unités UTF-16).
func truncUTF16(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s
	}
	return string(utf16.Decode(u[:n]))
}

func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}
