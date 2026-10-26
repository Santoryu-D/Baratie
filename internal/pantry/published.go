package pantry

// Baratie 0.6.0 : une publication faite depuis Baratie entre tout de suite
// dans le Garde-manger (sans attendre la revérification sur le tracker).

import (
	"os"
	"strings"
	"time"

	"baratie/internal/tracker"
)

// Posted : ce qui vient de partir.
type Posted struct {
	TMDB  int
	Name  string
	URL   string
	Paths []string // fichiers publiés (chemins sur le NAS)
	Sizes []int64
	// Category, Type, Resolution : libellés du tracker (pour la comparaison
	// des doublons).
	Category, Type, Resolution string
}

// MarkPublished ajoute la release aux versions du film : le fichier publié
// passe en « Identique » (publiée par toi), les autres copies du même film
// sont reclassées avec cette version.
func (m *Manager) MarkPublished(p Posted) {
	var total int64
	for _, s := range p.Sizes {
		total += s
	}
	v := tracker.Version{Name: p.Name, Size: total, URL: p.URL, Category: p.Category, Type: p.Type, Resolution: p.Resolution, TMDB: p.TMDB, Files: p.Sizes, Seeders: 1}
	// Le fichier publié, même par un autre chemin (lien physique de
	// downloads vers media) : même inode.
	var stats []os.FileInfo
	for _, path := range p.Paths {
		if st, err := os.Stat(path); err == nil {
			stats = append(stats, st)
		}
	}
	rules, _ := m.rules()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		mine := false
		for i, path := range p.Paths {
			if it.Path == path || (i < len(p.Sizes) && it.Size == p.Sizes[i] && sameFile(it.Path, stats)) {
				mine = true
				break
			}
		}
		if !mine && (p.TMDB == 0 || it.TMDB != p.TMDB) {
			continue
		}
		if mine {
			it.Published, it.PublishedAt = p.Name, time.Now()
			if it.TMDB == 0 && p.TMDB > 0 {
				it.TMDB, it.Source, it.Sure = p.TMDB, SrcManual, true
			}
		}
		seen := false
		for _, x := range it.Versions {
			if strings.EqualFold(x.Name, v.Name) {
				seen = true
				break
			}
		}
		if !seen {
			it.Versions = append(it.Versions, v)
		}
		if it.CheckedAt.IsZero() {
			// Jamais cherché : le tracker a au moins cette version ; la
			// recherche complète suivra au rythme habituel.
			it.CheckedAt = time.Now().Add(-Recheck + time.Hour)
		}
		m.reclass(it, rules)
		m.dirty = true
	}
}

func sameFile(path string, stats []os.FileInfo) bool {
	if len(stats) == 0 {
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, s := range stats {
		if os.SameFile(st, s) {
			return true
		}
	}
	return false
}
