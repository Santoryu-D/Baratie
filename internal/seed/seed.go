// Package seed : les clients torrent joignables et le suivi d'une release
// publiée, ajoutée À L'ARRÊT à son client sur les fichiers existants, vérifiée
// puis démarrée à 100 % (jamais de téléchargement : des données incomplètes
// laissent le torrent arrêté).
package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"baratie/internal/client"
	"baratie/internal/config"
	"baratie/internal/journal"
	"baratie/internal/pathmap"
	"baratie/internal/torrent"
	"baratie/internal/tracker"
)

// Target : un client torrent joignable.
type Target struct {
	ID      string
	Name    string // nom choisi, sinon le type du client
	Client  client.Client
	PathMap []pathmap.Rule // où Baratie voit les dossiers de ce client
	Host    string         // la machine du client (nom d'hôte de son adresse)
}

// Down : un client réglé mais injoignable.
type Down struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

// Env : le tracker, les réglages et les clients, relus à chaque usage.
type Env struct {
	Tracker tracker.Tracker
	Cfg     *config.Config
	Targets []Target // joignables, dans l'ordre des réglages
	Down    []Down   // injoignables
}

// Clients fournit l'environnement : erreur sans tracker ni client joignable.
type Clients func() (*Env, error)

// Get renvoie le client d'identifiant id (vide : le premier), nil s'il est
// injoignable.
func (env *Env) Get(id string) *Target {
	if id == "" {
		if env.Cfg != nil && len(env.Cfg.Clients) > 0 {
			id = env.Cfg.Clients[0].ID
		} else if len(env.Targets) > 0 {
			return &env.Targets[0]
		}
	}
	for i := range env.Targets {
		if env.Targets[i].ID == id {
			return &env.Targets[i]
		}
	}
	return nil
}

// Unreachable : pourquoi le client id n'est pas utilisable.
func (env *Env) Unreachable(id string) error {
	if id == "" && env.Cfg != nil && len(env.Cfg.Clients) > 0 {
		id = env.Cfg.Clients[0].ID
	}
	for _, d := range env.Down {
		if d.ID == id {
			return errors.New(DownText([]Down{d}))
		}
	}
	return errors.New("ce client torrent n'est plus dans les réglages")
}

// NoAnswer : le client n'a pas répondu à temps.
const NoAnswer = "pas de réponse : rien ne répond à temps à cette adresse (pare-feu qui bloque le port, ou adresse injoignable depuis la machine où tourne Baratie)"

// Reason : pourquoi un client ne répond pas, en clair.
func Reason(err error) string {
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err):
		return NoAnswer
	case errors.As(err, &op) && op.Op == "dial":
		return "connexion impossible : rien ne répond à cette adresse (client arrêté, interface web désactivée, machine éteinte, mauvaise adresse ou mauvais port)"
	}
	return err.Error()
}

// DownText : « Seedbox injoignable : … ; PC injoignable : … ».
func DownText(down []Down) string {
	parts := make([]string, 0, len(down))
	for _, d := range down {
		msg := d.Name + " injoignable"
		if e := strings.TrimRight(d.Error, ". "); e != "" {
			msg += " : " + e
		}
		parts = append(parts, msg)
	}
	return strings.Join(parts, " ; ")
}

// Listed : la liste d'un client joint.
type Listed struct {
	Target Target
	List   []client.Torrent
}

// ListAll lit la liste de chaque client, tous en même temps, chacun avec un
// délai.
func ListAll(ctx context.Context, env *Env) []Listed {
	res := make([]Listed, len(env.Targets))
	errs := make([]error, len(env.Targets))
	var wg sync.WaitGroup
	for i, t := range env.Targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			res[i].Target = t
			res[i].List, errs[i] = t.Client.List(c, nil)
		}(i, t)
	}
	wg.Wait()
	var out []Listed
	for i := range res {
		if errs[i] == nil {
			out = append(out, res[i])
		}
	}
	return out
}

// ---------- suivi des vérifications ----------

// États d'un ajout suivi.
const (
	StChecking = "checking"
	StSeeding  = "seeding"
	StFailed   = "failed"
	StRemoved  = "removed"
)

// WatchState : l'état d'un ajout suivi (ou terminé).
type WatchState struct {
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Detail   string  `json:"detail,omitempty"`
}

type watch struct {
	Client      string    `json:"client"`
	Name        string    `json:"name"`
	ID          string    `json:"id"`
	InfoHash    string    `json:"infoHash"`
	Since       time.Time `json:"since"`
	Status      string    `json:"status"`
	Detail      string    `json:"detail,omitempty"`
	Progress    float64   `json:"progress"`
	SawChecking bool      `json:"sawChecking,omitempty"`
	Rechecks    int       `json:"rechecks,omitempty"`
	RecheckAt   time.Time `json:"recheckAt,omitempty"`
	EndAt       time.Time `json:"endAt,omitempty"`
	EndProgress float64   `json:"endProgress,omitempty"`
	MissSince   time.Time `json:"missSince,omitempty"`
}

// Rythmes (raccourcis par les tests).
var (
	Every       = 3 * time.Second  // entre deux relevés
	recheckGap  = 30 * time.Second // avant de relancer une vérification jamais vue
	maxRechecks = 3
	endStable   = 20 * time.Second // fin sous 100 % stable avant de conclure
	missGrace   = 2 * time.Minute  // absent du client aussi longtemps avant de le dire retiré
)

// ErrInClient : ce torrent est déjà dans le client.
var ErrInClient = errors.New("ce torrent est déjà dans le client")

// Watcher suit les releases publiées jusqu'au seed.
type Watcher struct {
	clients Clients
	mu      sync.Mutex
	watches map[string]*watch
	final   map[string]WatchState
}

const pendingFile = "pending.json"

// New crée le suivi (relit les ajouts en cours) et le lance.
func New(c Clients) *Watcher {
	w := &Watcher{clients: c, watches: map[string]*watch{}, final: map[string]WatchState{}}
	if p, err := config.Path(pendingFile); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			var list []*watch
			if json.Unmarshal(b, &list) == nil {
				for _, x := range list {
					w.watches[x.InfoHash] = x
				}
			}
		}
	}
	go w.loop()
	return w
}

func (w *Watcher) saveLocked() {
	list := make([]*watch, 0, len(w.watches))
	for _, x := range w.watches {
		list = append(list, x)
	}
	if b, err := json.Marshal(list); err == nil {
		_ = config.WriteFileAtomic(pendingFile, b)
	}
}

// Busy : une vérification est en cours.
func (w *Watcher) Busy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, x := range w.watches {
		if x.Status == StChecking {
			return true
		}
	}
	return false
}

// WatchOf : l'état d'un ajout suivi, ou terminé depuis le lancement.
func (w *Watcher) WatchOf(hash string) (WatchState, bool) {
	hash = strings.ToLower(hash)
	w.mu.Lock()
	defer w.mu.Unlock()
	if x := w.watches[hash]; x != nil {
		return WatchState{Status: x.Status, Progress: x.Progress, Detail: x.Detail}, true
	}
	st, ok := w.final[hash]
	return st, ok
}

// AddAndVerify ajoute un .torrent à l'arrêt sur des données existantes, au
// client tg (savePath : vu par ce client), lance la vérification et le met
// sous surveillance.
func (w *Watcher) AddAndVerify(ctx context.Context, tg Target, cfg *config.Config, id string, raw []byte, savePath string) (string, error) {
	q := tg.Client
	t, err := torrent.Parse(raw)
	if err != nil {
		return "", errors.New("le .torrent reçu du tracker est illisible")
	}
	hash := strings.ToLower(t.InfoHash)
	if lst, err := q.List(ctx, []string{hash}); err == nil && len(lst) > 0 {
		return hash, ErrInClient
	}
	if err := q.Add(ctx, t.Name+".torrent", raw, client.AddOptions{SavePath: savePath, Category: cfg.Category, Tag: cfg.Tag}); err != nil {
		if errors.Is(err, client.ErrAlreadyThere) {
			return hash, ErrInClient
		}
		return "", err
	}
	found := false
	for i := 0; i < 30 && !found; i++ {
		if lst, err := q.List(ctx, []string{hash}); err == nil && len(lst) > 0 {
			found = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !found {
		return hash, errors.New("le client n'a pas confirmé l'ajout")
	}
	if err := q.Recheck(ctx, hash); err != nil {
		return hash, errors.New("vérification impossible à lancer : " + err.Error())
	}
	w.mu.Lock()
	w.watches[hash] = &watch{Client: tg.ID, Name: t.Name, ID: id, InfoHash: hash, Since: time.Now(), Status: StChecking}
	delete(w.final, hash)
	w.saveLocked()
	w.mu.Unlock()
	return hash, nil
}

func (w *Watcher) loop() {
	for {
		time.Sleep(Every)
		w.mu.Lock()
		byClient := map[string][]string{}
		for h, x := range w.watches {
			if x.Status == StChecking {
				byClient[x.Client] = append(byClient[x.Client], h)
			}
		}
		w.mu.Unlock()
		if len(byClient) == 0 {
			continue
		}
		env, err := w.clients()
		if err != nil {
			continue
		}
		for id, hashes := range byClient {
			t := env.Get(id)
			if t == nil && id != "" && env.Cfg != nil && env.Cfg.Client(id) == nil {
				for _, h := range hashes {
					w.set(h, StRemoved, "client retiré des réglages avant la fin de la vérification")
					w.forget(h)
				}
				continue
			}
			if t == nil {
				continue // client injoignable : on attend qu'il revienne
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			lst, err := t.Client.List(ctx, hashes)
			if err != nil {
				cancel()
				continue
			}
			byHash := map[string]client.Torrent{}
			for _, x := range lst {
				byHash[x.Hash] = x
				for _, h := range x.Hashes {
					byHash[h] = x
				}
			}
			for _, h := range hashes {
				w.step(ctx, t.Client, h, byHash)
			}
			cancel()
		}
		w.mu.Lock()
		w.saveLocked()
		w.mu.Unlock()
	}
}

func (w *Watcher) set(h, status, detail string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if x := w.watches[h]; x != nil {
		x.Status, x.Detail = status, detail
	}
}

func (w *Watcher) forget(h string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	x := w.watches[h]
	if x == nil {
		return
	}
	w.final[h] = WatchState{Status: x.Status, Progress: x.Progress, Detail: x.Detail}
	delete(w.watches, h)
}

func pct(p float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", p*100), ".", ",", 1) + " %"
}

func (w *Watcher) step(ctx context.Context, q client.Client, h string, byHash map[string]client.Torrent) {
	w.mu.Lock()
	x := w.watches[h]
	if x == nil {
		w.mu.Unlock()
		return
	}
	t, ok := byHash[h]
	if ok {
		x.MissSince = time.Time{}
	} else if x.MissSince.IsZero() {
		x.MissSince = time.Now()
	}
	missing := !ok && time.Since(x.MissSince) >= missGrace
	w.mu.Unlock()
	failed := func(detail string) {
		w.set(h, StFailed, detail)
		journal.Add(journal.Event{Kind: "seed_failed", Name: x.Name, InfoHash: h, ID: x.ID, Detail: detail})
	}
	switch {
	case !ok && !missing:
		// Peut-être un client qui redémarre : on attend avant de conclure.
	case !ok:
		w.set(h, StRemoved, "retiré du client")
		w.forget(h)
	case t.Downloading && t.Progress < 1:
		// Démarré seul alors que les données ne sont pas complètes : arrêté
		// avant tout téléchargement.
		_ = q.Stop(ctx, h)
		failed("données incomplètes (" + pct(t.Progress) + ") : arrêté avant tout téléchargement")
	case t.Checking:
		w.mu.Lock()
		x.SawChecking, x.Progress, x.EndAt = true, t.Progress, time.Time{}
		w.mu.Unlock()
	case t.Progress >= 1:
		if err := q.Start(ctx, h); err != nil {
			w.set(h, StChecking, "vérifié à 100 %, démarrage refusé : "+err.Error())
			return
		}
		w.mu.Lock()
		x.Progress = 1
		w.mu.Unlock()
		w.set(h, StSeeding, "")
		journal.Add(journal.Event{Kind: "seed", Name: x.Name, InfoHash: h, ID: x.ID, Detail: "vérifié à 100 %, en seed"})
		w.forget(h)
	case x.SawChecking:
		// Fin sous 100 % : retenue une fois stable.
		w.mu.Lock()
		if x.EndAt.IsZero() || t.Progress != x.EndProgress {
			x.EndAt, x.EndProgress = time.Now(), t.Progress
			w.mu.Unlock()
			return
		}
		stable := time.Since(x.EndAt) >= endStable
		w.mu.Unlock()
		if stable {
			failed("vérifié à " + pct(t.Progress) + " : les fichiers diffèrent du .torrent, il reste arrêté")
		}
	default:
		// Jamais vu en vérification : la demande s'est perdue, on la relance.
		w.mu.Lock()
		last := x.RecheckAt
		if last.IsZero() {
			last = x.Since
		}
		due, tries := time.Since(last) >= recheckGap, x.Rechecks
		w.mu.Unlock()
		if !due {
			return
		}
		if tries < maxRechecks {
			_ = q.Recheck(ctx, h)
			w.mu.Lock()
			x.Rechecks, x.RecheckAt = tries+1, time.Now()
			w.mu.Unlock()
			return
		}
		failed("ton client n'a pas lancé la vérification : lance-la à la main dans le client")
	}
}
