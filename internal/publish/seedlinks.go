package publish

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"baratie/internal/bencode"
	"baratie/internal/config"
	"baratie/internal/links"
	"baratie/internal/maketorrent"
	"baratie/internal/names"
)

// sameName : même nom de release, à la casse, aux points et à l'extension près.
func sameName(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		for _, ext := range []string{".mkv", ".mp4", ".avi", ".m2ts", ".ts", ".iso"} {
			s = strings.TrimSuffix(s, ext)
		}
		return strings.Join(names.Words(s), " ")
	}
	return norm(a) == norm(b)
}

func trackerStatus(s string) string {
	if s == "pending" {
		return "en attente de validation"
	}
	return "publiée"
}

// SeedDir : le dossier des liens de seed (réglage, sinon BARATIE_SEED_DIR).
func SeedDir(cfg *config.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.SeedDir) != "" {
		return filepath.Clean(strings.TrimSpace(cfg.SeedDir))
	}
	if d := strings.TrimSpace(os.Getenv("BARATIE_SEED_DIR")); d != "" {
		return filepath.Clean(d)
	}
	return ""
}

// SafeName : un titre de fiche utilisable comme nom de fichier (ni dossier,
// ni « .. », ni caractère de contrôle).
func SafeName(title string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r == '/' || r == '\\' || r == 0 || unicode.IsControl(r):
			b.WriteRune('.')
		case strings.ContainsRune(`<>:"|?*`, r):
			// gardés hors des noms (partages SMB, Windows)
		default:
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), ". ")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// seedPairs : les liens à créer. Un fichier seul devient « Titre.ext », un
// dossier devient « Titre/… » avec ses chemins intérieurs inchangés.
func seedPairs(dir string, plan *maketorrent.Plan, title string) (string, []links.Pair, error) {
	if plan == nil || len(plan.Files) == 0 {
		return "", nil, errors.New("release vide")
	}
	name := SafeName(title)
	if name == "" {
		name = SafeName(plan.Name)
	}
	if name == "" {
		return "", nil, errors.New("titre de fiche inutilisable comme nom de fichier")
	}
	var pairs []links.Pair
	if plan.Single {
		e := plan.Files[0]
		ext := filepath.Ext(e.Abs)
		if ext != "" && !strings.EqualFold(filepath.Ext(name), ext) {
			name += ext
		}
		pairs = append(pairs, links.Pair{Src: e.Abs, Dst: filepath.Join(dir, name), Size: e.Size})
		return name, pairs, nil
	}
	for _, e := range plan.Files {
		rel := filepath.FromSlash(e.Rel)
		pairs = append(pairs, links.Pair{Src: e.Abs, Dst: filepath.Join(dir, name, rel), Size: e.Size})
	}
	return name, pairs, nil
}

// makeLinks crée les liens physiques. Un lien déjà en place vers le même
// fichier (nouvel essai après un refus du site) est gardé ; tout autre fichier
// déjà là bloque : rien n'est jamais écrasé.
func makeLinks(dir string, pairs []links.Pair) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errors.New("dossier de seed « " + dir + " » impossible à créer : " + err.Error())
	}
	todo := pairs[:0:0]
	for _, p := range pairs {
		dst, err := os.Lstat(p.Dst)
		if err != nil {
			todo = append(todo, p)
			continue
		}
		src, err2 := os.Stat(p.Src)
		if err2 == nil && os.SameFile(src, dst) {
			continue
		}
		return errors.New("« " + p.Dst + " » existe déjà et n'est pas ce fichier : rien n'est écrasé, change le titre de la fiche")
	}
	if len(todo) == 0 {
		return nil
	}
	return links.Make(dir, todo)
}

// renameTorrent : le .torrent prend le nom des liens (info.name). Les pièces
// ne dépendent que du contenu : rien à rehacher.
func renameTorrent(raw []byte, name string) ([]byte, error) {
	d, err := bencode.Decode(raw)
	if err != nil {
		return nil, err
	}
	root, ok := d.Root.(bencode.Dict)
	if !ok {
		return nil, errors.New(".torrent illisible")
	}
	info, ok := root["info"].(bencode.Dict)
	if !ok {
		return nil, errors.New(".torrent sans info")
	}
	info["name"] = []byte(name)
	return bencode.Encode(root)
}

// SeedPreview : où la release sera seedée si elle part sous ce titre (le lien
// physique à créer), sans rien créer. Vide : seed sur les fichiers d'origine.
func (m *Manager) SeedPreview(id, title string) (string, error) {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil {
		m.mu.Unlock()
		return "", errors.New("publication inconnue")
	}
	plan := j.plan
	m.mu.Unlock()
	env, err := m.clients()
	if err != nil {
		return "", err
	}
	dir := SeedDir(env.Cfg)
	if dir == "" {
		return "", nil
	}
	name, _, err := seedPairs(dir, plan, title)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// MainFile : le chemin du plus gros fichier vidéo de la release (captures
// d'écran). Fichiers de ce PC seulement.
func (m *Manager) MainFile(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return "", errors.New("publication inconnue")
	}
	if j.MainFile < 0 || j.MainFile >= len(j.plan.Files) {
		return "", errors.New("aucun fichier vidéo dans cette release")
	}
	return j.plan.Files[j.MainFile].Abs, nil
}
