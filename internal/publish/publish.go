// Package publish mène une publication de bout en bout : liste des fichiers,
// doublon cherché AVANT de hacher, hachage en tâche de fond pendant que le
// membre remplit sa fiche, publication, puis seed sur les mêmes fichiers (ajout
// à l'arrêt, vérification, démarrage à 100 %).
package publish

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/V3x0rV3x/D3X/internal/audio"
	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/crossseed"
	"github.com/V3x0rV3x/D3X/internal/journal"
	"github.com/V3x0rV3x/D3X/internal/maketorrent"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/source"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// États d'une tâche.
const (
	StHashing    = "hashing"
	StReady      = "ready" // .torrent prêt
	StPublishing = "publishing"
	StSeeding    = "seeding" // publiée, ajout au client en cours ou fait
	StDone       = "done"    // publiée (seed suivi par le moteur de cross-seed)
	StError      = "error"
)

// Clients fournit les clients configurés.
type Clients func() (client.Client, *v3x.Client, *config.Config, error)

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
	MainFile    int      `json:"mainFile"` // index du plus gros fichier vidéo, sinon audio (MediaInfo)
	// Album : tags et qualité des pistes, quand la release en a.
	Audio     *audio.Album `json:"audio,omitempty"`
	Duplicate *v3x.Match   `json:"duplicate,omitempty"`
	State     string       `json:"state"`
	Hashed    int64        `json:"hashed"`
	FromCache bool         `json:"fromCache"`
	InfoHash  string       `json:"infoHash,omitempty"`
	Error     string       `json:"error,omitempty"`
	ErrorBody string       `json:"errorBody,omitempty"`
	// Où le client torrent voit le dossier qui contient la release.
	ClientDir string                `json:"clientDir"`
	MapGuess  *pathmap.Rule         `json:"mapGuess,omitempty"`
	MapVotes  int                   `json:"mapVotes"`
	Published *v3x.Uploaded         `json:"published,omitempty"`
	Seed      *crossseed.WatchState `json:"seed,omitempty"`
	SeedError string                `json:"seedError,omitempty"`

	// Source des fichiers (ce PC ou la seedbox) et son nom pour la page.
	Source string `json:"source"`

	plan    *maketorrent.Plan
	src     source.Source
	exclude []string
	raw     []byte
	cancel  context.CancelFunc
}

// Manager garde les publications de la session.
type Manager struct {
	clients Clients
	engine  *crossseed.Engine
	version string

	mu   sync.Mutex
	jobs map[string]*Job
}

// New crée le gestionnaire.
func New(c Clients, e *crossseed.Engine, version string) *Manager {
	return &Manager{clients: c, engine: e, version: version, jobs: map[string]*Job{}}
}

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m2ts": true, ".ts": true, ".mov": true, ".wmv": true, ".webm": true, ".mpg": true, ".mpeg": true, ".m4v": true, ".iso": true, ".vob": true}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start liste la release, cherche un doublon sur V3X et lance le hachage.
func (m *Manager) Start(src source.Source, where string) (*Job, error) {
	if err := private(src, where); err != nil {
		return nil, err
	}
	q, v, cfg, err := m.clients()
	if err != nil {
		return nil, err
	}
	ctx0, cancel0 := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel0()
	exclude := cfg.ExcludeList()
	plan, err := src.Scan(ctx0, where, exclude)
	if err != nil {
		return nil, err
	}
	j := &Job{ID: newID(), Path: plan.Root, Name: plan.Name, Single: plan.Single, Skipped: plan.Skipped, Total: plan.Total, PieceLength: plan.PieceLength, MainFile: -1, State: StHashing, plan: plan, src: src, exclude: exclude, Source: src.Label()}
	var best, bestAudio int64 = -1, -1
	mainAudio, hasAudio := -1, false
	for i, f := range plan.Files {
		isVideo := videoExt[strings.ToLower(path.Ext(f.Rel))]
		j.Files = append(j.Files, File{Rel: f.Rel, Size: f.Size, Video: isVideo})
		if isVideo && f.Size > best {
			best, j.MainFile = f.Size, i
		}
		if audio.IsAudio(f.Rel) {
			hasAudio = true
			if f.Size > bestAudio {
				bestAudio, mainAudio = f.Size, i
			}
		}
	}
	// Album (pas de vidéo) : tags et qualité lus là où sont les fichiers ;
	// MediaInfo sur la plus grosse piste.
	if j.MainFile < 0 && hasAudio {
		j.MainFile = mainAudio
		if a, err := src.Audio(ctx0, plan, exclude); err == nil {
			j.Audio = a
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Doublon : mêmes fichiers (ou mêmes tailles au-delà de 100 Mio) déjà sur V3X.
	if res, err := v.MatchBatch(ctx, nil, []v3x.MatchItem{{Key: "0", ContentHash: plan.ContentHash, SizeHash: plan.SizeHash, Total: plan.Total}}); err == nil {
		j.Duplicate = res["0"]
	}

	// Où le client voit le dossier parent : règle enregistrée, sinon devinée
	// d'après les torrents qu'il a déjà dans ce dossier, sinon le même chemin.
	parent := src.Dir(plan.Root)
	j.ClientDir = pathmap.ToClient(parent, cfg.PathMap)
	if j.ClientDir == parent {
		if list, err := q.List(ctx, nil); err == nil {
			paths := make([]string, 0, len(list))
			for _, t := range list {
				paths = append(paths, t.ContentPath)
			}
			if rule, n := pathmap.SuggestNames(parent, src.Names(ctx, parent), paths); n > 0 {
				j.MapVotes = n
				if rule.To != "" {
					j.MapGuess = &rule
					j.ClientDir = rule.To
				}
			}
		}
	}

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
	raw, cached, err := j.src.Hash(ctx, j.plan, j.exclude, "D3X "+m.version, func(done, total int64) {
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

// Busy dit si une publication est en route (hachage, envoi au site, seed).
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
	if c.Published != nil && c.InfoHash != "" {
		if st, ok := m.engine.WatchOf(c.InfoHash); ok {
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
	if j == nil || j.Published != nil || j.State == StPublishing {
		return
	}
	if j.cancel != nil {
		j.cancel()
	}
	delete(m.jobs, id)
}

// ReadFile lit un morceau d'un fichier de la release (MediaInfo dans la page),
// sur ce PC ou sur la seedbox. Seuls les fichiers de cette release sont lisibles.
func (m *Manager) ReadFile(ctx context.Context, id string, index int, off, n int64) ([]byte, int64, error) {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil || index < 0 || index >= len(j.plan.Files) {
		m.mu.Unlock()
		return nil, 0, errors.New("fichier inconnu")
	}
	f, src := j.plan.Files[index], j.src
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
	b, err := src.ReadAt(ctx, f.Abs, off, n)
	return b, f.Size, err
}

// Download renvoie les informations de la release pour la fiche.
func (m *Manager) Download(id string) (v3x.FicheDownload, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return v3x.FicheDownload{}, false
	}
	return v3x.FicheDownload{Name: j.Name, TotalSize: j.Total, FileCount: len(j.Files)}, true
}

// SetClientDir corrige le dossier vu par le client ; remember enregistre la
// règle pour les prochaines publications.
func (m *Manager) SetClientDir(id, dir string, remember bool) error {
	m.mu.Lock()
	j := m.jobs[id]
	if j == nil {
		m.mu.Unlock()
		return errors.New("publication inconnue")
	}
	j.ClientDir = strings.TrimSpace(dir)
	parent := j.src.Dir(j.Path)
	m.mu.Unlock()
	if !remember || j.ClientDir == "" || j.ClientDir == parent {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	rules := []pathmap.Rule{{From: parent, To: j.ClientDir}}
	for _, r := range cfg.PathMap {
		if r.From != parent {
			rules = append(rules, r)
		}
	}
	cfg.PathMap = rules
	return config.Save(cfg)
}

// Publish envoie la release au site puis la met en seed. fields : les champs
// de la fiche (mêmes noms que la page d'upload du site).
func (m *Manager) Publish(id string, fields map[string]string) error {
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
	j.State, j.Error, j.ErrorBody = StPublishing, "", ""
	raw, name := j.raw, j.Name
	m.mu.Unlock()

	go func() {
		q, v, cfg, err := m.clients()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var up *v3x.Uploaded
		if err == nil {
			up, err = v.Upload(ctx, name+".torrent", raw, fields)
		}
		if err != nil {
			m.mu.Lock()
			j.State, j.Error = StReady, err.Error()
			var ue *v3x.UploadError
			if errors.As(err, &ue) {
				j.Error, j.ErrorBody = ue.Code, string(ue.Body)
			}
			m.mu.Unlock()
			return
		}
		m.mu.Lock()
		j.Published, j.State = up, StSeeding
		if up.InfoHash != "" {
			j.InfoHash = strings.ToLower(up.InfoHash)
		}
		clientDir := j.ClientDir
		m.mu.Unlock()
		journal.Add(journal.Event{Kind: "published", Name: name, InfoHash: up.InfoHash, ID: up.ID, Detail: statusLabel(up.Status), Path: j.src.Key(j.Path)})

		// Seed : le .torrent du site (avec ta passkey), sur les fichiers d'origine.
		seedErr := ""
		torrentRaw, err := v.OwnTorrent(ctx, up.ID)
		if err == nil {
			_, err = m.engine.AddAndVerify(ctx, q, cfg, "pub:"+id, up.ID, torrentRaw, clientDir)
			if errors.Is(err, crossseed.ErrInClient) {
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

func statusLabel(s string) string {
	if s == "active" {
		return "publiée"
	}
	return "en attente de validation"
}

// private refuse les dossiers qui ne sont jamais des releases et gardent des
// secrets : celui de D3X (clé API, mot de passe du client) et ceux des clés
// SSH ou GPG.
func private(src source.Source, where string) error {
	norm := strings.ToLower(strings.ReplaceAll(where, `\`, "/"))
	for _, part := range strings.Split(norm, "/") {
		switch part {
		case ".ssh", ".gnupg", ".d3x":
			return errors.New("ce dossier n'est pas une release")
		}
	}
	if src.ID() == "local" {
		if dir, err := config.Dir(); err == nil {
			d := strings.ToLower(strings.ReplaceAll(dir, `\`, "/"))
			if norm == d || strings.HasPrefix(norm, strings.TrimRight(d, "/")+"/") {
				return errors.New("le dossier de D3X n'est pas une release")
			}
		}
	}
	return nil
}
