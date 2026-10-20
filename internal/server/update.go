package server

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"time"

	"github.com/V3x0rV3x/D3X/internal/update"
)

// Update annonce une version plus récente, prête ou non à être installée.
type Update struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Ready   bool   `json:"ready"`             // téléchargée ET vérifiée (signature, empreinte)
	Error   string `json:"error,omitempty"`   // téléchargement ou vérification refusés
	Managed string `json:"managed,omitempty"` // « docker » : à mettre à jour par l'image
}

// Repo est le dépôt public de D3X.
const Repo = update.Repo

// SetUpdateKey donne la clé publique qui signe les versions.
func (s *Server) SetUpdateKey(pub ed25519.PublicKey) {
	s.mu.Lock()
	s.pubKey = pub
	s.mu.Unlock()
}

// PageGone dit si la page a été fermée : elle a dit au revoir sans revenir
// depuis 8 s (un rechargement revient tout de suite), ou elle ne donne plus
// signe de vie depuis 3 min (une fenêtre réduite ne ralentit ses signaux qu'à
// un par minute). Faux tant que la page n'est jamais venue.
func (s *Server) PageGone() bool {
	last, bye := s.lastSeen.Load(), s.byeAt.Load()
	if last == 0 {
		return false
	}
	now := time.Now().UnixNano()
	if bye > 0 && bye >= last && now-bye > int64(8*time.Second) {
		return true
	}
	return now-last > int64(3*time.Minute)
}

// Busy dit si D3X travaille (ajouts, vérifications, publications) : pas de
// redémarrage ni d'arrêt à ce moment-là.
func (s *Server) Busy() bool { return s.engine.Busy() || s.pub.Busy() }

// updateEvery : une version sortie dans la soirée n'était vue que six heures
// plus tard (retour de V3x0r, 20/10/26).
const updateEvery = time.Hour

// updateLoop cherche une nouvelle version au lancement puis toutes les heures,
// la télécharge et la vérifie. Elle n'est installée qu'au redémarrage (bouton
// de la page ou prochain lancement), jamais au milieu d'un travail.
func (s *Server) updateLoop() {
	if s.Version == "dev" || s.Version == "" {
		return
	}
	time.Sleep(5 * time.Second)
	for {
		s.mu.Lock()
		enabled, pub := s.cfg.CheckUpdates, s.pubKey
		s.mu.Unlock()
		if enabled {
			s.checkUpdate(pub)
		}
		time.Sleep(updateEvery)
	}
}

func (s *Server) checkUpdate(pub ed25519.PublicKey) {
	s.lastUpdateCheck.Store(time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	rel, err := update.Latest(ctx, s.Version)
	if err != nil || !update.Newer(rel.Version, s.Version) {
		return
	}
	s.stageRelease(ctx, pub, rel)
}

// stageRelease annonce la version, puis la télécharge et la vérifie (sauf en
// Docker, où l'image se met à jour à part). Une version déjà prête n'est pas
// retéléchargée à chaque passage, et deux préparations ne tournent jamais en
// même temps.
func (s *Server) stageRelease(ctx context.Context, pub ed25519.PublicKey, rel *update.Release) {
	s.mu.Lock()
	if cur := s.update; cur != nil && cur.Version == rel.Version && cur.Ready {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	if !s.updateStaging.CompareAndSwap(false, true) {
		return
	}
	defer s.updateStaging.Store(false)
	u := &Update{Version: rel.Version, URL: rel.URL, Managed: s.opts.Managed}
	s.mu.Lock()
	s.update = u
	s.mu.Unlock()
	if pub == nil || s.opts.Managed != "" {
		return
	}
	done := &Update{Version: u.Version, URL: u.URL, Managed: u.Managed}
	if _, err := update.Stage(ctx, pub, rel); err != nil {
		done.Error = err.Error()
	} else {
		done.Ready = true
	}
	s.mu.Lock()
	s.update = done
	s.mu.Unlock()
}

// checkNow : bouton « Vérifier » à côté du numéro de version. Une recherche au
// plus par minute (l'API de GitHub limite les appels sans compte). La réponse
// part dès que la dernière version est connue ; si elle est plus récente, son
// téléchargement et sa vérification continuent en arrière-plan.
func (s *Server) checkNow(w http.ResponseWriter, r *http.Request) {
	out := map[string]interface{}{}
	if s.Version != "dev" && s.Version != "" && time.Since(time.Unix(0, s.lastUpdateCheck.Load())) >= time.Minute {
		s.lastUpdateCheck.Store(time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		rel, err := update.Latest(ctx, s.Version)
		cancel()
		if err != nil {
			fail(w, http.StatusBadGateway, errors.New("GitHub ne répond pas, réessaie dans un moment"))
			return
		}
		out["latest"] = rel.Version
		if update.Newer(rel.Version, s.Version) {
			s.mu.Lock()
			pub := s.pubKey
			s.mu.Unlock()
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
				defer cancel()
				s.stageRelease(ctx, pub, rel)
			}()
			// Annoncée tout de suite (« téléchargement en cours ») si elle ne l'est pas déjà.
			s.mu.Lock()
			if s.update == nil || s.update.Version != rel.Version {
				s.update = &Update{Version: rel.Version, URL: rel.URL, Managed: s.opts.Managed}
			}
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	out["version"], out["update"] = s.Version, s.update
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// Restart est fermé quand la page demande d'installer la mise à jour : main
// remplace le binaire, relance D3X sur la même session et s'arrête.
func (s *Server) RestartRequested() <-chan struct{} { return s.restart }

func (s *Server) restartNow(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	u := s.update
	s.mu.Unlock()
	if u == nil || !u.Ready {
		fail(w, http.StatusConflict, errors.New("aucune mise à jour vérifiée à installer"))
		return
	}
	if s.Busy() {
		fail(w, http.StatusConflict, errors.New("D3X travaille (ajouts, vérification ou publication) : réessaie quand c'est fini"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	s.restartOnce.Do(func() { close(s.restart) })
}
