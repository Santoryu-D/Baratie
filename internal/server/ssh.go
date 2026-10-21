package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/remote"
	"github.com/V3x0rV3x/D3X/internal/source"
)

// Seedbox par SSH. Le mot de passe (ou la phrase de passe de la clé) n'est
// jamais écrit : il vit en mémoire le temps de la session.

type sshPending struct {
	cfg    remote.Config
	secret string
	key    string // clé d'hôte vue, à accepter sur son empreinte
	fp     string
}

func (s *Server) routesSSH() {
	s.mux.HandleFunc("POST /ui/ssh/test", s.sshTest)
	s.mux.HandleFunc("POST /ui/ssh/accept", s.sshAccept)
	s.mux.HandleFunc("POST /ui/ssh/forget", s.sshForget)
}

// source renvoie la source demandée par la page (ce PC par défaut).
func (s *Server) source(id string) (source.Source, error) {
	if id != "ssh" {
		return source.Local{}, nil
	}
	s.mu.Lock()
	c := s.ssh
	s.mu.Unlock()
	if c == nil {
		return nil, errors.New("seedbox pas connectée : Réglages ▸ Seedbox (SSH) ▸ Se connecter")
	}
	return source.Remote{C: c}, nil
}

// connectSSH ouvre la seedbox enregistrée au lancement (clé SSH sans phrase
// de passe ; sinon la page demande le secret).
func (s *Server) connectSSH() {
	s.mu.Lock()
	cfg := s.cfg.SSH
	s.mu.Unlock()
	if cfg == nil || cfg.Host == "" {
		return
	}
	c := remote.New(*cfg, "", s.Version)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sys, err := c.Test(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		c.Close()
		// Sans clé ni mot de passe, ce n'est pas une panne : la page le redemande.
		if !errors.Is(err, remote.ErrNoAuth) {
			s.sshErr = err.Error()
		}
		return
	}
	s.ssh, s.sshSys, s.sshErr = c, sys, ""
}

func (s *Server) sshTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host    string `json:"host"`
		Port    int    `json:"port"`
		User    string `json:"user"`
		KeyPath string `json:"keyPath"`
		Secret  string `json:"secret"`
	}
	if !decode(w, r, 1<<14, &in) {
		return
	}
	cfg := remote.Config{Host: strings.TrimSpace(in.Host), Port: in.Port, User: strings.TrimSpace(in.User), KeyPath: strings.TrimSpace(in.KeyPath)}
	if cfg.Host == "" || cfg.User == "" {
		fail(w, http.StatusBadRequest, errors.New("indique l'adresse de la seedbox et ton utilisateur"))
		return
	}
	// Même machine qu'avant : la clé d'hôte déjà acceptée reste valable.
	s.mu.Lock()
	if old := s.cfg.SSH; old != nil && old.Host == cfg.Host && old.Port == cfg.Port {
		cfg.HostKey = old.HostKey
	} else {
		for _, o := range s.cfg.SSHSaved {
			if o.Host == cfg.Host && o.Port == cfg.Port && o.HostKey != "" {
				cfg.HostKey = o.HostKey
				break
			}
		}
	}
	s.mu.Unlock()
	s.trySSH(w, r, cfg, in.Secret)
}

// trySSH se connecte ; clé d'hôte inconnue ou changée : la page montre
// l'empreinte et le membre accepte (ou non).
func (s *Server) trySSH(w http.ResponseWriter, r *http.Request, cfg remote.Config, secret string) {
	c := remote.New(cfg, secret, s.Version)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sys, err := c.Test(ctx)
	if err != nil {
		c.Close()
		var hk *remote.HostKeyError
		if errors.As(err, &hk) {
			s.mu.Lock()
			s.sshPend = &sshPending{cfg: cfg, secret: secret, key: hk.Key, fp: hk.Fingerprint}
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]interface{}{"hostKey": map[string]interface{}{"fingerprint": hk.Fingerprint, "changed": hk.Changed}})
			return
		}
		fail(w, http.StatusBadGateway, err)
		return
	}
	saved := cfg
	if err := s.updateConfig(func(c *config.Config) {
		// La seedbox d'avant reste dans la liste (installations d'avant 1.1.18).
		if c.SSH != nil {
			c.RememberSSH(*c.SSH)
		}
		c.SSH = &saved
		c.RememberSSH(saved)
	}); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	if s.ssh != nil {
		s.ssh.Close()
	}
	s.ssh, s.sshSys, s.sshErr, s.sshPend = c, sys, "", nil
	s.mu.Unlock()
	s.state(w, r)
}

func (s *Server) sshAccept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fingerprint string `json:"fingerprint"`
	}
	if !decode(w, r, 1<<12, &in) {
		return
	}
	s.mu.Lock()
	p := s.sshPend
	s.mu.Unlock()
	if p == nil || p.fp != in.Fingerprint {
		fail(w, http.StatusConflict, errors.New("rien à accepter : refais le test de connexion"))
		return
	}
	cfg := p.cfg
	cfg.HostKey = p.key
	s.trySSH(w, r, cfg, p.secret)
}

func (s *Server) sshForget(w http.ResponseWriter, r *http.Request) {
	if err := s.updateConfig(func(c *config.Config) {
		if c.SSH != nil {
			c.ForgetSSH(*c.SSH)
		}
		c.SSH = nil
	}); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	if s.ssh != nil {
		s.ssh.Close()
	}
	s.ssh, s.sshSys, s.sshErr, s.sshPend = nil, "", "", nil
	s.mu.Unlock()
	s.state(w, r)
}

// updateConfig modifie et enregistre la configuration. La liste des clients
// est copiée : f peut la modifier sans toucher la configuration en cours.
func (s *Server) updateConfig(f func(*config.Config)) error {
	s.mu.Lock()
	c := *s.cfg
	c.Clients = append([]config.ClientConfig(nil), c.Clients...)
	f(&c)
	s.cfg = &c
	s.mu.Unlock()
	return config.Save(&c)
}

// sshSavedOut : une seedbox enregistrée, telle que la page la propose.
type sshSavedOut struct {
	Label   string `json:"label"`
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user"`
	KeyPath string `json:"keyPath,omitempty"`
}
