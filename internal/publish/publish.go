// Package publish mène une publication de bout en bout : liste des fichiers,
// ce que le tracker a déjà sous ce nom, hachage en tâche de fond pendant que
// la fiche se remplit, publication (un clic = un envoi), puis seed sur les
// mêmes fichiers : liens physiques au nom de la fiche, .torrent du site ajouté
// à l'arrêt, vérifié, démarré à 100 %.
package publish

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"baratie/internal/client"
	"baratie/internal/config"
	"baratie/internal/journal"
	"baratie/internal/maketorrent"
	"baratie/internal/pathmap"
	"baratie/internal/seed"
	"baratie/internal/torrent"
	"baratie/internal/tracker"
)

// États d'une tâche.
const (
	StHashing    = "hashing"
	StReady      = "ready" // .torrent prêt
	StPublishing = "publishing"
	StSeeding    = "seeding" // publiée, ajout au client en cours
	StDone       = "done"    // publiée, seed suivi
	StError      = "error"
)

// File est un fichier de la release, tel que la page le voit.
type File struct {
	Rel   string `json:"rel"`
	Size  int64  `json:"size"`
	Video bool   `json:"video"`
}

// Job est une publication en cours.
type Job struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Name        string   `json:"name"`
	Single      bool     `json:"single"`
	Files       []File   `json:"files"`
	Skipped     []string `json:"skipped,omitempty"`
	Total       int64    `json:"total"`
	PieceLength int64    `json:"pieceLength"`
	MainFile    int      `json:"mainFile"` // index du plus gros fichier vidéo (MediaInfo, captures)
	State       string   `json:"state"`
	Hashed      int64    `json:"hashed"`
	FromCache   bool     `json:"fromCache"`
	InfoHash    string   `json:"infoHash,omitempty"`
	Error       string   `json:"error,omitempty"`
	ErrorBody   string   `json:"errorBody,omitempty"`
	// Le client torrent qui seedera la release, et où il voit le dossier qui
	// la contient.
	Client     string        `json:"client"`
	ClientName string        `json:"clientName"`
	ClientDir  string        `json:"clientDir"`
	MapGuess   *pathmap.Rule `json:"mapGuess,omitempty"`
	MapVotes   int           `json:"mapVotes"`
	// Le tracker visé, ce qu'il a déjà sous ce nom, la publication faite.
	Tracker  string            `json:"tracker,omitempty"`
	Existing []tracker.Version `json:"existing,omitempty"`
	Posted   *tracker.Uploaded `json:"posted,omitempty"`
	// SeedLink : le lien physique d'où la release est seedée.
	SeedLink  string           `json:"seedLink,omitempty"`
	Seed      *seed.WatchState `json:"seed,omitempty"`
	SeedError string           `json:"seedError,omitempty"`

	plan    *maketorrent.Plan
	exclude []string
	raw     []byte
	cancel  context.CancelFunc
}

// Manager garde les publications de la session.
type Manager struct {
	clients seed.Clients
	watcher *seed.Watcher
	version string

	mu   sync.Mutex
	jobs map[string]*Job

	// OnPosted : appelé après une publication acceptée par le tracker (le
	// Garde-manger la prend en compte tout de suite).
	OnPosted func(f tracker.Fields, up *tracker.Uploaded, paths []string, sizes []int64)
}

// New crée le gestionnaire.
func New(c seed.Clients, w *seed.Watcher, version string) *Manager {
	return &Manager{clients: c, watcher: w, version: version, jobs: map[string]*Job{}}
}

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m2ts": true, ".ts": true, ".mov": true, ".wmv": true, ".webm": true, ".mpg": true, ".mpeg": true, ".m4v": true, ".iso": true, ".vob": true}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start liste la release, cherche son nom sur le tracker et lance le hachage.
func (m *Manager) Start(where string) (*Job, error) {
	if err := private(where); err != nil {
		return nil, err
	}
	env, err := m.clients()
	if err != nil {
		return nil, err
	}
	exclude := env.Cfg.ExcludeList()
	plan, err := maketorrent.Scan(where, exclude)
	if err != nil {
		return nil, err
	}
	j := &Job{ID: newID(), Path: plan.Root, Name: plan.Name, Single: plan.Single, Skipped: plan.Skipped, Total: plan.Total, PieceLength: plan.PieceLength, MainFile: -1, State: StHashing, plan: plan, exclude: exclude}
	var best int64 = -1
	for i, f := range plan.Files {
		isVideo := videoExt[strings.ToLower(path.Ext(f.Rel))]
		j.Files = append(j.Files, File{Rel: f.Rel, Size: f.Size, Video: isVideo})
		if isVideo && f.Size > best {
			best, j.MainFile = f.Size, i
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if env.Tracker != nil {
		trackerExisting(ctx, env.Tracker, j)
	}
	// Le client qui seedera, et où il voit le dossier parent.
	j.setPlace(pick(ctx, env, filepath.Dir(plan.Root)))

	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()
	go m.hash(j)
	return j, nil
}

func (m *Manager) hash(j *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	j.cancel = cancel
	m.mu.Unlock()
	raw, cached, err := j.plan.Hash(ctx, "Baratie "+m.version, func(done, total int64) {
		m.mu.Lock()
		j.Hashed = done
		m.mu.Unlock()
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		if j.State == StHashing {
			j.State, j.Error = StError, err.Error()
		}
		return
	}
	j.raw, j.FromCache, j.Hashed = raw, cached, j.Total
	if t, err := torrent.Parse(raw); err == nil {
		j.InfoHash = strings.ToLower(t.InfoHash)
	}
	if j.State == StHashing {
		j.State = StReady
	}
}

// Busy dit si une publication est en route (hachage, envoi, seed).
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.State == StHashing || j.State == StPublishing || j.State == StSeeding {
			return true
		}
	}
	return false
}

// Get renvoie une copie de la tâche, avec l'état du seed.
func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil {
		m.mu.Unlock()
		return nil, false
	}
	c := *j
	m.mu.Unlock()
	if c.Posted != nil && c.InfoHash != "" && m.watcher != nil {
		if st, ok := m.watcher.WatchOf(c.InfoHash); ok {
			c.Seed = &st
		}
	}
	return &c, true
}

// Cancel abandonne une publication pas encore envoyée.
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil || j.Posted != nil || j.State == StPublishing {
		return
	}
	if j.cancel != nil {
		j.cancel()
	}
	delete(m.jobs, id)
}

// ReadFile lit un morceau d'un fichier de la release (MediaInfo dans la
// page). Seuls les fichiers de cette release sont lisibles.
func (m *Manager) ReadFile(id string, index int, off, n int64) ([]byte, int64, error) {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil || index < 0 || index >= len(j.plan.Files) {
		m.mu.Unlock()
		return nil, 0, errors.New("fichier inconnu")
	}
	f := j.plan.Files[index]
	m.mu.Unlock()
	if off < 0 || off > f.Size || n <= 0 {
		return nil, f.Size, nil
	}
	if n > 32<<20 {
		n = 32 << 20
	}
	if off+n > f.Size {
		n = f.Size - off
	}
	fh, err := os.Open(f.Abs)
	if err != nil {
		return nil, f.Size, err
	}
	defer fh.Close()
	b, err := io.ReadAll(io.NewSectionReader(fh, off, n))
	return b, f.Size, err
}

// SetClientDir corrige le dossier vu par le client. Renvoie la règle à
// retenir pour les prochaines publications (nil si le chemin est le même des
// deux côtés) et le client qu'elle concerne.
func (m *Manager) SetClientDir(id, dir string) (*pathmap.Rule, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return nil, "", errors.New("publication inconnue")
	}
	if j.Posted != nil || j.State == StPublishing {
		return nil, "", errors.New("release déjà envoyée : son dossier ne change plus")
	}
	j.ClientDir = strings.TrimSpace(dir)
	j.MapGuess = nil
	parent := filepath.Dir(j.Path)
	if j.ClientDir == "" || j.ClientDir == parent {
		return nil, j.Client, nil
	}
	return &pathmap.Rule{From: parent, To: j.ClientDir}, j.Client, nil
}

// SetClient change le client qui seedera la release (avant l'envoi).
func (m *Manager) SetClient(id, clientID string) error {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil {
		m.mu.Unlock()
		return errors.New("publication inconnue")
	}
	if j.Posted != nil || j.State == StPublishing {
		m.mu.Unlock()
		return errors.New("release déjà envoyée : elle reste dans son client")
	}
	parent := filepath.Dir(j.Path)
	m.mu.Unlock()
	env, err := m.clients()
	if err != nil {
		return err
	}
	t := env.Get(clientID)
	if t == nil {
		return env.Unreachable(clientID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var list []client.Torrent
	if !pathmap.Covers(parent, t.PathMap) {
		list, _ = t.Client.List(ctx, nil)
	}
	pl := place(*t, list, parent, dirNames(parent))
	m.mu.Lock()
	j.setPlace(pl)
	m.mu.Unlock()
	return nil
}

// placement : un client et où il voit le dossier d'une release.
type placement struct {
	t     seed.Target
	dir   string
	guess *pathmap.Rule
	votes int
}

func (j *Job) setPlace(p placement) {
	j.Client, j.ClientName, j.ClientDir, j.MapGuess, j.MapVotes = p.t.ID, p.t.Name, p.dir, p.guess, p.votes
}

// dirNames : les noms du dossier (pour deviner la correspondance des chemins).
func dirNames(dir string) []string {
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.Name())
	}
	return out
}

// place : où le client t voit le dossier parent. Règle enregistrée, sinon
// devinée d'après les torrents qu'il a déjà dans ce dossier, sinon le même
// chemin.
func place(t seed.Target, list []client.Torrent, parent string, names []string) placement {
	p := placement{t: t, dir: parent}
	if pathmap.Covers(parent, t.PathMap) {
		p.dir = pathmap.ToClient(parent, t.PathMap)
		return p
	}
	if len(list) == 0 {
		return p
	}
	paths := make([]string, 0, len(list))
	for _, x := range list {
		paths = append(paths, x.ContentPath)
	}
	if rule, n := pathmap.SuggestNames(parent, names, paths); n > 0 {
		p.votes = n
		if rule.To != "" {
			p.guess = &rule
			p.dir = rule.To
		}
	}
	return p
}

// pick choisit le client qui seedera une release du dossier parent : celui
// dont la correspondance de dossiers couvre ce dossier ; sinon celui qui
// seede déjà le plus de torrents rangés dans ce dossier ; sinon un client de
// cette machine ; sinon le premier.
func pick(ctx context.Context, env *seed.Env, parent string) placement {
	for _, t := range env.Targets {
		if pathmap.Covers(parent, t.PathMap) {
			return place(t, nil, parent, nil)
		}
	}
	lists := seed.ListAll(ctx, env)
	nm := dirNames(parent)
	var best *placement
	for _, l := range lists {
		p := place(l.Target, l.List, parent, nm)
		if p.votes > 0 && (best == nil || p.votes > best.votes) {
			best = &p
		}
	}
	if best != nil {
		return *best
	}
	for _, t := range env.Targets {
		h := strings.ToLower(strings.Trim(t.Host, "[]"))
		if h == "127.0.0.1" || h == "localhost" || h == "::1" {
			return place(t, nil, parent, nil)
		}
	}
	return place(env.Targets[0], nil, parent, nil)
}

// private refuse les dossiers qui ne sont jamais des releases et gardent des
// secrets : celui de Baratie (jetons, mots de passe) et ceux des clés SSH ou
// GPG.
func private(where string) error {
	norm := strings.ToLower(strings.ReplaceAll(where, `\`, "/"))
	for _, part := range strings.Split(norm, "/") {
		switch part {
		case ".ssh", ".gnupg":
			return errors.New("ce dossier n'est pas une release")
		}
	}
	if dir, err := config.Dir(); err == nil {
		d := strings.ToLower(strings.ReplaceAll(dir, `\`, "/"))
		if norm == d || strings.HasPrefix(norm, strings.TrimRight(d, "/")+"/") {
			return errors.New("le dossier de Baratie n'est pas une release")
		}
	}
	return nil
}

// PublishTo envoie la release au tracker (un clic = un envoi), puis la met
// en seed sur les mêmes fichiers avec le .torrent du site.
func (m *Manager) PublishTo(id string, f tracker.Fields, nfo []byte) error {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil {
		m.mu.Unlock()
		return errors.New("publication inconnue")
	}
	if j.State != StReady || j.raw == nil {
		state := j.State
		m.mu.Unlock()
		if state == StHashing {
			return errors.New("le .torrent n'est pas encore prêt : attends la fin du hachage")
		}
		return errors.New("cette publication n'est plus disponible")
	}
	for _, v := range j.Existing {
		if v.Same {
			m.mu.Unlock()
			return errors.New("une release de ce nom est déjà sur le site")
		}
	}
	j.State, j.Error, j.ErrorBody = StPublishing, "", ""
	raw, name, clientID, plan := j.raw, j.Name, j.Client, j.plan
	m.mu.Unlock()

	env, err := m.clients()
	if err != nil {
		m.fail(j, err)
		return err
	}
	if env.Tracker == nil {
		err := errors.New("aucun tracker n'est relié")
		m.fail(j, err)
		return err
	}
	t := env.Tracker

	// Dossier de seed : la release y devient des liens physiques au nom de la
	// fiche (les fichiers d'origine ne sont ni touchés ni renommés), AVANT tout
	// envoi : si les liens sont impossibles, rien ne part.
	if dir := SeedDir(env.Cfg); dir != "" {
		linkName, pairs, err := seedPairs(dir, plan, f.Name)
		if err == nil {
			err = makeLinks(dir, pairs)
		}
		if err == nil {
			raw, err = renameTorrent(raw, linkName)
		}
		if err != nil {
			err = errors.New("seed : " + err.Error())
			m.fail(j, err)
			return err
		}
		clientDir := dir
		if tg := env.Get(clientID); tg != nil && pathmap.Covers(dir, tg.PathMap) {
			clientDir = pathmap.ToClient(dir, tg.PathMap)
		}
		m.mu.Lock()
		j.ClientDir, j.SeedLink, name = clientDir, filepath.Join(dir, linkName), strings.TrimSuffix(linkName, filepath.Ext(linkName))
		if !plan.Single {
			name = linkName
		}
		m.mu.Unlock()
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		up, err := t.Upload(ctx, name+".torrent", raw, nfo, f)
		if err != nil {
			m.fail(j, err)
			return
		}
		m.mu.Lock()
		j.Posted, j.State = up, StSeeding
		clientDir := j.ClientDir
		m.mu.Unlock()
		journal.Add(journal.Event{Kind: "published", Name: f.Name, ID: up.ID, Detail: t.Name() + " · " + trackerStatus(up.Status), Path: j.Path})
		if m.OnPosted != nil {
			var paths []string
			var sizes []int64
			for _, e := range plan.Files {
				paths, sizes = append(paths, e.Abs), append(sizes, e.Size)
			}
			m.OnPosted(f, up, paths, sizes)
		}

		// Seed : le .torrent du site (réécrit par lui : nouvelle empreinte),
		// ajouté à l'arrêt sur les fichiers, vérifié, démarré.
		seedErr := ""
		siteRaw, err := t.SeedTorrent(ctx, up)
		if err == nil {
			if st, perr := torrent.Parse(siteRaw); perr == nil {
				m.mu.Lock()
				j.InfoHash = strings.ToLower(st.InfoHash)
				m.mu.Unlock()
			}
			if tg := env.Get(clientID); tg == nil {
				err = env.Unreachable(clientID)
			} else {
				_, err = m.watcher.AddAndVerify(ctx, *tg, env.Cfg, up.ID, siteRaw, clientDir)
			}
			if errors.Is(err, seed.ErrInClient) {
				err = nil
			}
		}
		if err != nil {
			seedErr = err.Error()
		}
		m.mu.Lock()
		j.State, j.SeedError = StDone, seedErr
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) fail(j *Job, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.State, j.Error = StReady, err.Error()
	var ue *tracker.UploadError
	if errors.As(err, &ue) {
		j.Error, j.ErrorBody = ue.Error(), ue.Body
	}
}

// trackerExisting : ce que le tracker a déjà sous ce nom (un même nom serait
// refusé par le site). Les autres versions du même film viennent une fois le
// film TMDB choisi.
func trackerExisting(ctx context.Context, t tracker.Tracker, j *Job) {
	j.Tracker = t.ID()
	vs, err := t.Existing(ctx, tracker.ExistingQuery{Name: j.Name})
	if err != nil {
		return
	}
	for i := range vs {
		vs[i].Same = sameName(vs[i].Name, j.Name)
	}
	j.Existing = vs
}
