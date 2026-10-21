// Package links range une release sous d'autres noms sans rien copier : ses
// fichiers sont des liens physiques vers ceux d'une copie existante (même
// disque, aucune place en plus, la copie n'est pas touchée), ou des liens
// symboliques quand la copie ne peut pas être liée physiquement (partage en
// lecture seule, autre disque).
//
// Garde-fous : jamais rien d'écrasé (un lien ne se crée que là où il n'y a
// rien), tout reste sous le dossier des liens, et un retrait n'efface un
// fichier que s'il est la même donnée que sa source (la source reste).
package links

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Pair : Dst devient un lien physique vers Src (chemins absolus). Avec Target,
// Dst devient un lien symbolique dont le contenu est Target : le chemin de Src
// tel que le client torrent le voit (c'est lui qui suit le lien).
type Pair struct {
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Size   int64  `json:"size"`
	Target string `json:"target,omitempty"`
}

// inside : p est-il sous base (après nettoyage) ?
func inside(p, base string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Make crée les liens. base doit exister. Au premier échec, ce qui a été créé
// est retiré : soit tout est en place, soit rien.
func Make(base string, pairs []Pair) error {
	st, err := os.Stat(base)
	if err != nil || !st.IsDir() {
		return errors.New("le dossier des liens « " + base + " » n'existe pas : crée-le d'abord")
	}
	for _, p := range pairs {
		if !inside(p.Dst, base) {
			return errors.New("« " + p.Dst + " » sortirait du dossier des liens")
		}
		fi, err := os.Lstat(p.Src)
		if err != nil {
			return errors.New("fichier de ta copie introuvable : " + p.Src)
		}
		if !fi.Mode().IsRegular() {
			return errors.New("« " + p.Src + " » n'est pas un fichier ordinaire")
		}
		if p.Size > 0 && fi.Size() != p.Size {
			return errors.New("« " + p.Src + " » n'a pas la taille attendue")
		}
		if _, err := os.Lstat(p.Dst); err == nil {
			return errors.New("« " + p.Dst + " » existe déjà : rien n'est écrasé")
		}
	}
	var made []Pair
	var dirs []string
	undo := func() {
		for _, p := range made {
			_ = removeIfSame(p)
		}
		pruneDirs(base, dirs)
	}
	for _, p := range pairs {
		dir := filepath.Dir(p.Dst)
		if _, err := os.Stat(dir); err != nil {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				undo()
				return errors.New("dossier impossible à créer : " + err.Error())
			}
			dirs = append(dirs, dir)
		}
		var err error
		if p.Target != "" {
			err = os.Symlink(p.Target, p.Dst)
		} else {
			err = os.Link(p.Src, p.Dst)
		}
		if err != nil {
			undo()
			return explain(err, p.Target != "")
		}
		made = append(made, p)
	}
	return nil
}

func explain(err error, symbolic bool) error {
	s := strings.ToLower(err.Error())
	if symbolic {
		if strings.Contains(s, "privilege") || strings.Contains(s, "privilège") {
			return errors.New("Windows refuse les liens symboliques : active le mode développeur (Paramètres ▸ Système ▸ Pour les développeurs) ou lance D3X en administrateur")
		}
		return errors.New("lien symbolique impossible : " + err.Error())
	}
	switch {
	case strings.Contains(s, "cross-device"), strings.Contains(s, "not same device"), strings.Contains(s, "different disk"), strings.Contains(s, "pas le même"):
		return errors.New("le dossier des liens doit être sur le même disque que tes fichiers")
	case strings.Contains(s, "not supported"), strings.Contains(s, "operation not permitted"), strings.Contains(s, "incorrect function"):
		return errors.New("ce disque n'accepte pas les liens physiques : " + err.Error())
	}
	return errors.New("lien impossible : " + err.Error())
}

// Remove retire des liens créés par Make : un fichier n'est effacé que s'il
// est la même donnée que sa source, qui reste en place. Les dossiers vidés
// sont retirés, jamais base.
func Remove(base string, pairs []Pair) error {
	var dirs []string
	var first error
	for _, p := range pairs {
		if !inside(p.Dst, base) {
			continue
		}
		if err := removeIfSame(p); err != nil && first == nil {
			first = err
		}
		dirs = append(dirs, filepath.Dir(p.Dst))
	}
	pruneDirs(base, dirs)
	return first
}

func removeIfSame(p Pair) error {
	dst, err := os.Lstat(p.Dst)
	if err != nil {
		return nil // déjà parti
	}
	// Lien symbolique : retiré s'il vise bien ce fichier (son contenu est le
	// chemin donné à sa création), sans rien demander à la source, qui peut
	// être un partage débranché.
	if dst.Mode()&os.ModeSymlink != 0 {
		if t, err := os.Readlink(p.Dst); err == nil && p.Target != "" && t == p.Target {
			return os.Remove(p.Dst)
		}
		return errors.New("« " + p.Dst + " » n'est pas un lien vers ta copie : laissé en place")
	}
	src, err := os.Stat(p.Src)
	if err != nil {
		return errors.New("source introuvable, « " + p.Dst + " » laissé en place")
	}
	if !os.SameFile(src, dst) {
		return errors.New("« " + p.Dst + " » n'est pas un lien vers ta copie : laissé en place")
	}
	return os.Remove(p.Dst)
}

// pruneDirs retire les dossiers vides (et leurs parents vides) sous base.
func pruneDirs(base string, dirs []string) {
	seen := map[string]bool{}
	var all []string
	for _, d := range dirs {
		for x := filepath.Clean(d); inside(x, base); x = filepath.Dir(x) {
			if !seen[x] {
				seen[x] = true
				all = append(all, x)
			}
		}
	}
	// Les plus profonds d'abord.
	sort.Slice(all, func(i, j int) bool { return len(all[i]) > len(all[j]) })
	for _, d := range all {
		_ = os.Remove(d) // échoue sans rien faire s'il n'est pas vide
	}
}
