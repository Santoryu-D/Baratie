// Package pantry : le Garde-manger de Baratie. Il parcourt les bibliothèques
// de films (lecture seule), identifie chaque film sur TMDB (Radarr d'abord,
// sinon par le nom), regarde ce que le tracker en a déjà, et classe chaque
// film : absent du tracker, complément, déjà couvert, identique, exclu.
//
// Il ne publie rien et n'écrit nulle part ailleurs que dans son cache (dossier
// de configuration).
package pantry

import (
	"crypto/sha1"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultLibs : les bibliothèques scannées si rien n'est réglé (aucune : à choisir dans l'onglet).
var DefaultLibs []string

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m2ts": true, ".ts": true, ".m4v": true, ".wmv": true, ".mov": true, ".mpg": true, ".mpeg": true, ".iso": true, ".vob": true, ".webm": true}

// Les fichiers d'à-côté : jamais des films.
var (
	extraDir  = regexp.MustCompile(`(?i)^(extras?|bonus|featurettes?|behind the scenes|deleted scenes|interviews?|trailers?|samples?|shorts?|scenes|other|subs?|plex versions|optimized for (tv|mobile)|@eadir)$`)
	extraFile = regexp.MustCompile(`(?i)(^|[ ._\-\[(])(sample|trailer|teaser|featurette|extra|bonus)([ ._\-\])]|$)`)
)

// MinFilmSize : en dessous, un fichier vidéo n'est pas un film (bande-annonce,
// sample oublié…).
const MinFilmSize = 200 << 20

// Found : un film trouvé sur le disque.
type Found struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Lib     string    `json:"lib"`
	Rel     string    `json:"rel"` // chemin dans la bibliothèque
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// IDOf : identifiant stable d'un chemin.
func IDOf(path string) string {
	h := sha1.Sum([]byte(path))
	return hex.EncodeToString(h[:8])
}

// Scan parcourt les bibliothèques : chaque fichier vidéo assez gros, hors
// dossiers et fichiers d'à-côté, est un film. Lecture seule.
func Scan(libs []string) ([]Found, []string) {
	var out []Found
	var errs []string
	for _, lib := range libs {
		lib = filepath.Clean(lib)
		st, err := os.Stat(lib)
		if err != nil || !st.IsDir() {
			errs = append(errs, "bibliothèque introuvable : "+lib)
			continue
		}
		_ = filepath.WalkDir(lib, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // dossier illisible : on passe
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") && p != lib {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p != lib && extraDir.MatchString(name) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !videoExt[strings.ToLower(filepath.Ext(name))] || extraFile.MatchString(strings.TrimSuffix(name, filepath.Ext(name))) {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() < MinFilmSize {
				return nil
			}
			rel, _ := filepath.Rel(lib, p)
			out = append(out, Found{ID: IDOf(p), Path: p, Lib: filepath.Base(lib), Rel: filepath.ToSlash(rel), Size: info.Size(), ModTime: info.ModTime()})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, errs
}

// NameFor : le nom qui décrit le film. Le nom du fichier, sauf s'il ne dit
// pas l'année et que son dossier la dit (« Titre (2010)/film.mkv »).
func NameFor(f Found, parse func(string) (title, year string)) string {
	base := strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
	if _, y := parse(base); y != "" {
		return base
	}
	dir := filepath.Base(filepath.Dir(f.Path))
	if dir != f.Lib {
		if _, y := parse(dir); y != "" {
			return dir
		}
	}
	return base
}
