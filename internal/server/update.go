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

// updateLoop cherche une nouvelle version au lancement puis toutes les 6 h, la
// télécharge et la vérifie. Elle n'est installée qu'au redémarrage (bouton de
// la page ou prochain lancement), jamais au milieu d'un travail.
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
		time.Sleep(6 * time.Hour)
	}
}

func (s *Server) checkUpdate(pub ed25519.PublicKey) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	rel, err := update.Latest(ctx, s.Version)
	if err != nil || !update.Newer(rel.Version, s.Version) {
		return
	}
	u := &Update{Version: rel.Version, URL: rel.URL, Managed: s.opts.Managed}
	s.mu.Lock()
	s.update = u
	s.mu.Unlock()
	if pub == nil || s.opts.Managed != "" {
		return
	}
	if _, err := update.Stage(ctx, pub, rel); err != nil {
		u.Error = err.Error()
	} else {
		u.Ready = true
	}
	s.mu.Lock()
	s.update = u
	s.mu.Unlock()
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
