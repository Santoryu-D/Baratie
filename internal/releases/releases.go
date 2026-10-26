// Package releases liste les dossiers où vivent les releases (sélecteur de la
// page Publier).
package releases

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"baratie/internal/maketorrent"
)

// FsEntry est une entrée d'un dossier.
type FsEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size,omitempty"` // fichiers seulement : un dossier n'est jamais mesuré ici
	Skip bool   `json:"skip,omitempty"` // écarté des releases (caché, aperçus de médiathèque…)
}

// Listing est le contenu d'un dossier. Parent vaut nil à la racine des points
// de départ, "" pour revenir à ces points de départ.
type Listing struct {
	Path    string    `json:"path"`
	Parent  *string   `json:"parent"`
	Entries []FsEntry `json:"entries"`
}

// Browse liste un dossier. Sans chemin : les points de départ (lecteurs sous
// Windows ; dossier perso, racine et montages ailleurs).
func Browse(path string, exclude []string) (*Listing, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return &Listing{Path: "", Parent: nil, Entries: roots()}, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, errors.New("dossier introuvable")
	}
	if !st.IsDir() {
		abs = filepath.Dir(abs)
	}
	list, err := os.ReadDir(abs)
	if err != nil {
		return nil, errors.New("lecture refusée : " + err.Error())
	}
	out := make([]FsEntry, 0, len(list))
	for _, e := range list {
		full := filepath.Join(abs, e.Name())
		st, err := os.Stat(full) // suit les liens
		if err != nil {
			continue
		}
		ent := FsEntry{Name: e.Name(), Path: full, Dir: st.IsDir(), Skip: maketorrent.Excluded(e.Name(), exclude)}
		if !ent.Dir {
			ent.Size = st.Size()
		}
		out = append(out, ent)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	parent := ""
	if up := filepath.Dir(abs); up != abs {
		parent = up
	}
	return &Listing{Path: abs, Parent: &parent, Entries: out}, nil
}

func roots() []FsEntry {
	var out []FsEntry
	// Dans l'image Docker, le dossier personnel est /config (réglages, jeton) :
	// on part des médias montés.
	if home, err := os.UserHomeDir(); err == nil && os.Getenv("BARATIE_DOCKER") == "" {
		out = append(out, FsEntry{Name: "Dossier personnel", Path: home, Dir: true})
	}
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			d := string(c) + `:\`
			if _, err := os.Stat(d); err == nil {
				out = append(out, FsEntry{Name: "Lecteur " + string(c) + ":", Path: d, Dir: true})
			}
		}
		return out
	}
	out = append(out, FsEntry{Name: "/", Path: "/", Dir: true})
	for _, m := range []string{"/mnt", "/media", "/Volumes", "/srv", "/data", "/home"} {
		if st, err := os.Stat(m); err == nil && st.IsDir() {
			out = append(out, FsEntry{Name: m, Path: m, Dir: true})
		}
	}
	return out
}
