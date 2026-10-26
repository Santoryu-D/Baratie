package pantry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"baratie/internal/config"
	"baratie/internal/fiche"
	"baratie/internal/names"
	"baratie/internal/tmdb"
	"baratie/internal/tracker"
)

// Sources de l'identification TMDB.
const (
	SrcRadarr = "radarr"
	SrcName   = "name"
	SrcManual = "manual"
)

// Item : un film du Garde-manger.
type Item struct {
	Found
	Name       string            `json:"name"` // nom qui décrit le film (fichier ou dossier)
	TMDB       int               `json:"tmdb,omitempty"`
	TMDBTitle  string            `json:"tmdbTitle,omitempty"`
	TMDBYear   string            `json:"tmdbYear,omitempty"`
	Poster     string            `json:"poster,omitempty"`
	Source     string            `json:"source,omitempty"` // radarr, name, manual
	Sure       bool              `json:"sure"`
	Candidates []tmdb.Movie      `json:"candidates,omitempty"`
	Versions   []tracker.Version `json:"versions,omitempty"`
	CheckedAt  time.Time         `json:"checkedAt,omitempty"`
	Ignored    bool              `json:"ignored,omitempty"`
	Err        string            `json:"err,omitempty"`
	Class      string            `json:"class"`
	Why        string            `json:"why"`

	// 0.6.0 : langue originale et animation (TMDB ou Radarr), lecture du
	// fichier par ffprobe, publication faite depuis Baratie.
	OrigLang    string           `json:"origLang,omitempty"`
	Animation   bool             `json:"animation,omitempty"`
	LangChecked bool             `json:"langChecked,omitempty"`
	Probe       *fiche.MediaInfo `json:"probe,omitempty"`
	ProbeKey    string           `json:"probeKey,omitempty"` // taille + date du fichier lu
	ProbeErr    string           `json:"probeErr,omitempty"`
	Published   string           `json:"published,omitempty"` // nom publié par toi
	PublishedAt time.Time        `json:"publishedAt,omitempty"`
}

// probeKey : un fichier relu seulement s'il a changé.
func (it *Item) probeKey() string {
	return fmt.Sprintf("%d/%d", it.Size, it.ModTime.Unix())
}

// Ext : l'extension du fichier.
func (it *Item) Ext() string { return filepath.Ext(it.Path) }

// Deps : ce dont le Garde-manger a besoin, relu à chaque usage.
type Deps struct {
	Cfg     func() config.Config
	Tracker func() tracker.Tracker // nil : pas de tracker relié
	TMDB    func() *tmdb.Client    // nil : pas de clé TMDB
	// FFprobe : le chemin de ffprobe (vide : pas de lecture des fichiers).
	FFprobe func() string
}

// State : où en est le Garde-manger.
type State struct {
	Phase     string    `json:"phase"` // idle, scan, radarr, identify
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	Paused    bool      `json:"paused"`
	Checking  string    `json:"checking,omitempty"` // film en cours de recherche sur le tracker
	Queue     int       `json:"queue"`              // films à chercher sur le tracker
	Rate      int       `json:"rate"`               // requêtes par minute
	Errors    []string  `json:"errors,omitempty"`
	LastScan  time.Time `json:"lastScan,omitempty"`
	LastError string    `json:"lastError,omitempty"`
	Radarr    string    `json:"radarr,omitempty"` // état de Radarr (nombre de films ou erreur)
	// Lecture des fichiers (ffprobe) : en cours, faits, restants.
	Probing  string `json:"probing,omitempty"`
	Probed   int    `json:"probed"`
	ToProbe  int    `json:"toProbe"`
	ProbeOff string `json:"probeOff,omitempty"` // pourquoi la lecture est impossible
}

// Recheck : une recherche sur le tracker est refaite au-delà de ce délai.
const Recheck = 7 * 24 * time.Hour

// Manager : le Garde-manger.
type Manager struct {
	deps Deps

	mu    sync.Mutex
	items map[string]*Item
	st    State
	dirty bool
	wake  chan struct{}
	pwake chan struct{} // réveil de la lecture des fichiers

	// Pour les tests : délai entre deux recherches (sinon d'après le rythme).
	interval time.Duration
	tmdbGap  time.Duration
}

// New crée le Garde-manger, relit son cache et lance la recherche en fond.
func New(d Deps) *Manager {
	m := &Manager{deps: d, items: map[string]*Item{}, wake: make(chan struct{}, 1), pwake: make(chan struct{}, 1), tmdbGap: 250 * time.Millisecond}
	m.st.Phase = "idle"
	m.load()
	go m.worker()
	go m.prober()
	go m.saver()
	return m
}

const cacheFile = "garde-manger.json"

func (m *Manager) load() {
	p, err := config.Path(cacheFile)
	if err != nil {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var c struct {
		LastScan time.Time `json:"lastScan"`
		Items    []*Item   `json:"items"`
	}
	if json.Unmarshal(b, &c) != nil {
		return
	}
	m.st.LastScan = c.LastScan
	for _, it := range c.Items {
		m.items[it.ID] = it
	}
}

// saver écrit le cache au plus toutes les 5 s quand il a changé.
func (m *Manager) saver() {
	for {
		time.Sleep(5 * time.Second)
		m.Flush()
	}
}

// Flush écrit le cache s'il a changé.
func (m *Manager) Flush() {
	m.mu.Lock()
	if !m.dirty {
		m.mu.Unlock()
		return
	}
	c := struct {
		LastScan time.Time `json:"lastScan"`
		Items    []*Item   `json:"items"`
	}{LastScan: m.st.LastScan}
	for _, it := range m.items {
		cp := *it
		c.Items = append(c.Items, &cp)
	}
	m.dirty = false
	m.mu.Unlock()
	sort.Slice(c.Items, func(i, j int) bool { return c.Items[i].Path < c.Items[j].Path })
	if b, err := json.Marshal(c); err == nil {
		_ = config.WriteFileAtomic(cacheFile, b)
	}
}

func (m *Manager) rules() (fiche.Rules, bool) {
	t := m.deps.Tracker()
	if t == nil {
		return fiche.Rules{Tracker: "le tracker"}, false
	}
	r, ok := t.(interface{ Rules() fiche.Rules })
	if !ok {
		return fiche.Rules{Tracker: t.Name()}, false
	}
	return r.Rules(), true
}

// reclass recalcule la classe d'un film (verrou tenu).
func (m *Manager) reclass(it *Item, rules fiche.Rules) {
	it.Class, it.Why = Classify(it, rules)
}

// Libs : les bibliothèques réglées.
func Libs(cfg config.Config) []string {
	if len(cfg.PantryLibs) > 0 {
		return cfg.PantryLibs
	}
	return DefaultLibs
}

// Rate : requêtes par minute vers le tracker.
func Rate(cfg config.Config) int {
	if cfg.TrackerRate > 0 && cfg.TrackerRate <= 30 {
		return cfg.TrackerRate
	}
	return 20
}

// Scan lance un passage complet : disque, Radarr, TMDB. Les recherches sur le
// tracker suivent en fond, au rythme réglé.
func (m *Manager) Scan() error {
	m.mu.Lock()
	if m.st.Phase != "idle" {
		m.mu.Unlock()
		return errors.New("un scan est déjà en cours")
	}
	if len(Libs(m.deps.Cfg())) == 0 {
		m.mu.Unlock()
		return errors.New("choisis d'abord les dossiers à scanner (« Choisir les dossiers »)")
	}
	m.st.Phase, m.st.Done, m.st.Total, m.st.Errors, m.st.Radarr = "scan", 0, 0, nil, ""
	m.mu.Unlock()
	go m.run()
	return nil
}

func (m *Manager) setPhase(p string, total int) {
	m.mu.Lock()
	m.st.Phase, m.st.Done, m.st.Total = p, 0, total
	m.mu.Unlock()
}

func (m *Manager) run() {
	cfg := m.deps.Cfg()
	found, errs := Scan(Libs(cfg))
	rules, _ := m.rules()

	m.mu.Lock()
	m.st.Errors = errs
	seen := map[string]bool{}
	for _, f := range found {
		seen[f.ID] = true
		it := m.items[f.ID]
		if it == nil {
			it = &Item{}
			m.items[f.ID] = it
		} else if it.Size != f.Size {
			// Fichier remplacé (Radarr…) : à revoir sur le tracker.
			it.CheckedAt, it.Versions = time.Time{}, nil
		}
		it.Found = f
		it.Name = NameFor(f, func(s string) (string, string) { r := fiche.Parse(s); return r.Title, r.Year })
		m.reclass(it, rules)
	}
	libs := Libs(cfg)
	for id, it := range m.items {
		// Disparu d'une bibliothèque scannée : retiré. Ceux d'une bibliothèque
		// décochée restent en mémoire (cachés), prêts si elle revient.
		if !seen[id] && under(it.Path, libs) {
			delete(m.items, id)
		}
	}
	m.st.LastScan = time.Now()
	m.dirty = true
	m.mu.Unlock()

	// Radarr : l'identification la plus sûre.
	if cfg.RadarrURL != "" && cfg.RadarrKey != "" {
		m.setPhase("radarr", 1)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		idx, err := NewRadarr(cfg.RadarrURL, cfg.RadarrKey).Index(ctx)
		cancel()
		m.mu.Lock()
		if err != nil {
			m.st.Radarr = err.Error()
		} else {
			n := 0
			for _, it := range m.items {
				if it.Source == SrcManual {
					continue
				}
				if id := idx.Lookup(it.Path); id > 0 {
					if it.TMDB != id {
						it.CheckedAt, it.Versions = time.Time{}, nil
						it.OrigLang, it.Animation, it.LangChecked = "", false, false
					}
					it.TMDB, it.Source, it.Sure, it.Candidates = id, SrcRadarr, true, nil
					if inf, ok := idx.Info[id]; ok && inf.Lang != "" {
						it.OrigLang, it.Animation, it.LangChecked = inf.Lang, inf.Anim, true
					}
					n++
				}
			}
			m.st.Radarr = plural(n, "film reconnu", "films reconnus") + " par Radarr"
			m.dirty = true
		}
		m.mu.Unlock()
	}

	// TMDB par le nom, pour les autres.
	m.identify(rules)

	m.mu.Lock()
	for _, it := range m.items {
		m.reclass(it, rules)
	}
	m.st.Phase = "idle"
	m.dirty = true
	m.mu.Unlock()
	m.kick()
	m.pkick()
}

func (m *Manager) identify(rules fiche.Rules) {
	m.mu.Lock()
	var todo []*Item
	for _, it := range m.items {
		if it.TMDB == 0 && len(it.Candidates) == 0 && it.Source != SrcManual && !it.Ignored && it.Class != ClassExcluded {
			todo = append(todo, it)
		}
	}
	m.mu.Unlock()
	sort.Slice(todo, func(i, j int) bool { return todo[i].Path < todo[j].Path })
	m.setPhase("identify", len(todo))
	c := m.deps.TMDB()
	if c == nil {
		if len(todo) > 0 {
			m.mu.Lock()
			m.st.LastError = "clé TMDB manquante : les films hors Radarr ne peuvent pas être identifiés"
			m.mu.Unlock()
		}
		return
	}
	for _, it := range todo {
		m.mu.Lock()
		name := it.Name
		m.mu.Unlock()
		r := fiche.Parse(name)
		var res []tmdb.Movie
		var err error
		if r.Title != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			res, err = c.Search(ctx, r.Title, r.Year)
			cancel()
		}
		m.mu.Lock()
		switch {
		case err != nil:
			it.Err = err.Error()
		default:
			it.Err = ""
			if best, sure := pickMovie(r.Title, r.Year, res); sure {
				it.TMDB, it.TMDBTitle, it.TMDBYear, it.Poster, it.Source, it.Sure = best.ID, best.Title, best.Year, best.Poster, SrcName, true
				it.OrigLang, it.Animation, it.LangChecked = best.OrigLang, best.Animation, best.OrigLang != ""
				it.Candidates = nil
			} else {
				if len(res) > 3 {
					res = res[:3]
				}
				it.Candidates = res
			}
		}
		m.reclass(it, rules)
		m.st.Done++
		m.dirty = true
		m.mu.Unlock()
		time.Sleep(m.tmdbGap)
	}
}

// pickMovie : le film sûr parmi les résultats (même titre, même année), sinon
// rien.
func pickMovie(title, year string, res []tmdb.Movie) (tmdb.Movie, bool) {
	for i, mv := range res {
		if i >= 5 {
			break
		}
		same := names.Similarity(title, mv.Title) >= 0.99 || names.Similarity(title, mv.OriginalTitle) >= 0.99
		if same && year != "" && mv.Year == year {
			return mv, true
		}
	}
	// Sans année : un seul résultat au titre exact.
	if year == "" && len(res) == 1 && (names.Similarity(title, res[0].Title) >= 0.99 || names.Similarity(title, res[0].OriginalTitle) >= 0.99) {
		return res[0], true
	}
	return tmdb.Movie{}, false
}

// kick réveille la recherche en fond.
func (m *Manager) kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// next : le prochain film à chercher sur le tracker (jamais cherché d'abord,
// puis le plus ancien).
func (m *Manager) next() *Item {
	libs := Libs(m.deps.Cfg())
	var best *Item
	for _, it := range m.items {
		if it.TMDB == 0 || it.Ignored || it.Class == ClassExcluded || !under(it.Path, libs) {
			continue
		}
		if !it.CheckedAt.IsZero() && time.Since(it.CheckedAt) < Recheck {
			continue
		}
		if best == nil || it.CheckedAt.Before(best.CheckedAt) || (it.CheckedAt.Equal(best.CheckedAt) && it.Path < best.Path) {
			best = it
		}
	}
	return best
}

func (m *Manager) queue() int {
	libs := Libs(m.deps.Cfg())
	n := 0
	for _, it := range m.items {
		if !under(it.Path, libs) {
			continue
		}
		if it.TMDB > 0 && !it.Ignored && it.Class != ClassExcluded && (it.CheckedAt.IsZero() || time.Since(it.CheckedAt) >= Recheck) {
			n++
		}
	}
	return n
}

// worker : une recherche sur le tracker à la fois, au rythme réglé ; jamais
// d'envoi, seulement des lectures.
func (m *Manager) worker() {
	for {
		cfg := m.deps.Cfg()
		gap := time.Minute / time.Duration(Rate(cfg))
		if m.interval > 0 {
			gap = m.interval
		}
		m.mu.Lock()
		paused := m.st.Paused
		var it *Item
		if !paused {
			it = m.next()
		}
		var tmdbID int
		if it != nil {
			tmdbID = it.TMDB
			m.st.Checking = it.Name
		}
		m.mu.Unlock()
		t := m.deps.Tracker()
		if it == nil || t == nil {
			m.mu.Lock()
			m.st.Checking = ""
			m.mu.Unlock()
			select {
			case <-m.wake:
			case <-time.After(30 * time.Second):
			}
			continue
		}
		rules, _ := m.rules()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		vs, err := t.Existing(ctx, tracker.ExistingQuery{TMDB: tmdbID, Categories: []int{rules.CatFilms, rules.CatFilmsVOSTFR}})
		cancel()
		m.mu.Lock()
		if cur := m.items[it.ID]; cur == it && it.TMDB == tmdbID {
			if err != nil {
				it.Err = err.Error()
				m.st.LastError = err.Error()
				// Erreur : on ne revient pas sur ce film avant une heure.
				it.CheckedAt = time.Now().Add(-Recheck + time.Hour)
				it.Versions = nil
			} else {
				it.Err, it.Versions, it.CheckedAt = "", vs, time.Now()
				m.st.LastError = ""
			}
			m.reclass(it, rules)
			m.dirty = true
		}
		m.st.Checking = ""
		m.mu.Unlock()
		// Limite presque atteinte (en-têtes du site) : une minute de pause.
		if r, ok := t.(interface{ Remaining() (int, bool) }); ok {
			if n, known := r.Remaining(); known && n <= 2 {
				gap = time.Minute
			}
		}
		if err != nil {
			gap = time.Minute
		}
		time.Sleep(gap)
	}
}

// State : l'état pour la page.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st
	s.Queue = m.queue()
	s.Rate = Rate(m.deps.Cfg())
	s.Probed, s.ToProbe = m.probeCounts()
	return s
}

// under : le chemin est-il dans une des bibliothèques ?
func under(p string, libs []string) bool {
	for _, l := range libs {
		l = filepath.Clean(l)
		if strings.HasPrefix(p, l+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Items : les films des bibliothèques réglées, classés.
func (m *Manager) Items() []Item {
	libs := Libs(m.deps.Cfg())
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Item, 0, len(m.items))
	for _, it := range m.items {
		if under(it.Path, libs) {
			out = append(out, *it)
		}
	}
	order := map[string]int{ClassAbsent: 0, ClassComplete: 1, ClassMaybe: 2, ClassEvaluate: 3, ClassUnknown: 4, ClassPending: 5, ClassCovered: 6, ClassIdentical: 7, ClassExcluded: 8}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Ignored != b.Ignored {
			return !a.Ignored
		}
		if order[a.Class] != order[b.Class] {
			return order[a.Class] < order[b.Class]
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out
}

// Get : un film.
func (m *Manager) Get(id string) (Item, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it := m.items[id]
	if it == nil {
		return Item{}, false
	}
	return *it, true
}

// SetMovie : le film TMDB choisi à la main (0 = l'oublier). Recherché sur le
// tracker au prochain tour.
func (m *Manager) SetMovie(id string, mv tmdb.Movie) error {
	rules, _ := m.rules()
	m.mu.Lock()
	defer m.mu.Unlock()
	it := m.items[id]
	if it == nil {
		return errors.New("film inconnu")
	}
	it.TMDB, it.TMDBTitle, it.TMDBYear, it.Poster = mv.ID, mv.Title, mv.Year, mv.Poster
	it.OrigLang, it.Animation, it.LangChecked = mv.OrigLang, mv.Animation, mv.OrigLang != ""
	it.Source, it.Sure, it.Candidates = SrcManual, mv.ID > 0, nil
	if mv.ID == 0 {
		it.Source = ""
	}
	it.CheckedAt, it.Versions, it.Err = time.Time{}, nil, ""
	m.reclass(it, rules)
	m.dirty = true
	go m.kick()
	return nil
}

// Ignore retire un film de la liste (ou l'y remet).
func (m *Manager) Ignore(id string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it := m.items[id]
	if it == nil {
		return errors.New("film inconnu")
	}
	it.Ignored = on
	m.dirty = true
	return nil
}

// Pause arrête (ou reprend) les recherches sur le tracker.
func (m *Manager) Pause(on bool) {
	m.mu.Lock()
	m.st.Paused = on
	m.mu.Unlock()
	if !on {
		m.kick()
	}
}

// Recheck : refaire la recherche d'un film sur le tracker au prochain tour.
func (m *Manager) Recheck(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it := m.items[id]
	if it == nil {
		return errors.New("film inconnu")
	}
	it.CheckedAt = time.Time{}
	m.dirty = true
	go m.kick()
	return nil
}

func plural(n int, one, many string) string {
	if n > 1 {
		return itoa(n) + " " + many
	}
	return itoa(n) + " " + one
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
