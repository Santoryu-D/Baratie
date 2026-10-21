package crossseed

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// Plusieurs clients torrent : chacun a ses dossiers et sa correspondance de
// chemins. Le moteur les interroge tous, fusionne ce qu'ils seedent, et ajoute
// chaque release au client qui a déjà ses fichiers, avec SON chemin. Un client
// injoignable est signalé et n'empêche pas de travailler avec les autres.

// Target : un client torrent joignable.
type Target struct {
	ID       string
	Name     string // nom choisi par le membre, sinon le type du client
	Client   client.Client
	PathMap  []pathmap.Rule // où D3X voit les dossiers de ce client
	LinksDir string         // dossier des liens physiques, vu par ce client
	Host     string         // la machine du client (nom d'hôte de son adresse)
}

// Down : un client réglé mais injoignable.
type Down struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

// Env : V3X, les réglages et les clients, relus à chaque usage (les réglages
// changent).
type Env struct {
	V3X     *v3x.Client
	Cfg     *config.Config
	Targets []Target // joignables, dans l'ordre des réglages
	Down    []Down   // injoignables
}

// Clients fournit l'environnement : erreur si V3X n'est pas joint, ou si
// aucun client ne l'est.
type Clients func() (*Env, error)

// Get renvoie le client d'identifiant id, nil s'il est injoignable. Vide : le
// premier client des réglages (ajouts suivis par une version qui n'en
// connaissait qu'un, médiathèque sans choix).
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

// Reason : pourquoi un client ne répond pas, en clair quand c'est la machine
// ou le réseau (client arrêté, machine éteinte), sinon l'erreur du client.
func Reason(err error) string {
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err):
		return "pas de réponse"
	case errors.As(err, &op) && op.Op == "dial":
		return "connexion impossible : rien ne répond à cette adresse (client arrêté, interface web désactivée ou mal réglée, machine éteinte, mauvaise adresse ou mauvais port)"
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

// listTimeout : le plus long qu'on attende la liste d'un client.
var listTimeout = 3 * time.Minute

// Listed : la liste d'un client joint.
type Listed struct {
	Target Target
	List   []client.Torrent
}

// ListAll lit la liste de chaque client, tous en même temps et chacun avec un
// délai : un client injoignable ne retarde ni ne bloque les autres, il est
// rendu dans down (avec ceux déjà injoignables avant l'analyse).
func ListAll(ctx context.Context, env *Env) ([]Listed, []Down) {
	res := make([]Listed, len(env.Targets))
	errs := make([]error, len(env.Targets))
	var wg sync.WaitGroup
	for i, t := range env.Targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, listTimeout)
			defer cancel()
			res[i].Target = t
			res[i].List, errs[i] = t.Client.List(c, nil)
		}(i, t)
	}
	wg.Wait()
	var out []Listed
	down := append([]Down(nil), env.Down...)
	for i := range res {
		if errs[i] != nil {
			down = append(down, Down{ID: res[i].Target.ID, Name: res[i].Target.Name, Error: Reason(errs[i])})
			continue
		}
		out = append(out, res[i])
	}
	return out, down
}

// Refresh : de quoi retenter les clients injoignables au début d'une analyse
// (le client d'une seedbox redémarré depuis le lancement de D3X).
type Refresh func(ctx context.Context)

// SetRefresh branche la nouvelle tentative de connexion.
func (e *Engine) SetRefresh(f Refresh) {
	e.mu.Lock()
	e.refresh = f
	e.mu.Unlock()
}

func (e *Engine) retry(ctx context.Context) {
	e.mu.Lock()
	f := e.refresh
	e.mu.Unlock()
	if f != nil {
		f(ctx)
	}
}
