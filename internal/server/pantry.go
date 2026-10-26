package server

// Baratie : routes du Garde-manger (internal/pantry).

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"baratie/internal/config"
	"baratie/internal/fiche"
	"baratie/internal/pantry"
	"baratie/internal/shots"
	"baratie/internal/tmdb"
	"baratie/internal/tracker"
)

// pantryIn : réglages du Garde-manger envoyés par la page.
type pantryIn struct {
	Libs      []string `json:"libs"`
	RadarrURL string   `json:"radarrUrl"`
	RadarrKey string   `json:"radarrKey"` // vide = inchangée
	RadarrOff bool     `json:"radarrOff"` // oublier Radarr
	Rate      int      `json:"rate"`
}

func (p *pantryIn) apply(c *config.Config) {
	var libs []string
	for _, l := range p.Libs {
		if l = strings.TrimSpace(l); l != "" {
			libs = append(libs, l)
		}
	}
	c.PantryLibs = libs
	c.RadarrURL = strings.TrimSpace(p.RadarrURL)
	if k := strings.TrimSpace(p.RadarrKey); k != "" {
		c.RadarrKey = k
	}
	if p.RadarrOff {
		c.RadarrURL, c.RadarrKey = "", ""
	}
	if p.Rate >= 1 && p.Rate <= 30 {
		c.TrackerRate = p.Rate
	} else {
		c.TrackerRate = 0
	}
}

// pantryOut : réglages du Garde-manger pour la page (jamais la clé Radarr).
type pantryOut struct {
	Libs      []string `json:"libs"`
	RadarrURL string   `json:"radarrUrl"`
	RadarrKey bool     `json:"radarrKey"`
	Rate      int      `json:"rate"`
}

func pantrySettings(c config.Config) pantryOut {
	return pantryOut{Libs: pantry.Libs(c), RadarrURL: c.RadarrURL, RadarrKey: c.RadarrKey != "", Rate: pantry.Rate(c)}
}

func (s *Server) newPantry() *pantry.Manager {
	return pantry.New(pantry.Deps{
		Cfg: func() config.Config {
			s.mu.Lock()
			defer s.mu.Unlock()
			return *s.cfg
		},
		Tracker: func() tracker.Tracker {
			if t := s.connectedTracker(); t != nil {
				return t
			}
			return nil
		},
		TMDB: func() *tmdb.Client {
			s.mu.Lock()
			key := s.cfg.TMDBKey
			s.mu.Unlock()
			if key == "" {
				key = tmdbFallbackKey
			}
			if key == "" {
				return nil
			}
			return tmdb.New(key)
		},
		FFprobe: func() string {
			if t, err := shots.Find(); err == nil {
				return t.FFprobe
			}
			return ""
		},
	})
}

// pantryPosted : une publication acceptée entre tout de suite dans le
// Garde-manger (0.6.0).
func (s *Server) pantryPosted(f tracker.Fields, up *tracker.Uploaded, paths []string, sizes []int64) {
	if s.pantry == nil || up == nil {
		return
	}
	p := pantry.Posted{TMDB: f.TMDB, Name: f.Name, URL: up.URL, Paths: paths, Sizes: sizes, Category: "Films"}
	if t := s.connectedTracker(); t != nil {
		if r, ok := t.(interface{ Rules() fiche.Rules }); ok {
			rules := r.Rules()
			if f.CategoryID == rules.CatFilmsVOSTFR && rules.CatFilmsVOSTFR != 0 {
				p.Category = "Films VOSTFR"
			}
			for kind, id := range rules.Types {
				if id == f.TypeID {
					p.Type = map[string]string{fiche.TypeRemux: "Remux", fiche.TypeEncode: "Encode", fiche.TypeWEB: "WEB", fiche.TypeHDTV: "HDTV", fiche.TypeFullDisc: "Full Disc", fiche.TypeDVDR: "DVDR", fiche.Type3D: "3D"}[kind]
				}
			}
			for name, id := range rules.Resolutions {
				if id == f.ResolutionID {
					p.Resolution = name
				}
			}
		}
	}
	s.pantry.MarkPublished(p)
}

func (s *Server) routesPantry() {
	s.pantry = s.newPantry()
	s.pub.OnPosted = s.pantryPosted
	s.mux.HandleFunc("GET /ui/pantry", s.pantryList)
	s.mux.HandleFunc("POST /ui/pantry/scan", s.pantryScan)
	s.mux.HandleFunc("POST /ui/pantry/pause", s.pantryPause)
	s.mux.HandleFunc("POST /ui/pantry/libs", s.pantryLibs)
	s.mux.HandleFunc("POST /ui/pantry/{id}/movie", s.pantryMovie)
	s.mux.HandleFunc("POST /ui/pantry/{id}/ignore", s.pantryIgnore)
	s.mux.HandleFunc("POST /ui/pantry/{id}/recheck", s.pantryRecheck)
}

func (s *Server) pantryList(w http.ResponseWriter, r *http.Request) {
	st := s.pantry.State()
	name := "le tracker"
	if t := s.connectedTracker(); t != nil {
		name = t.Name()
	}
	s.mu.Lock()
	libs := pantry.Libs(*s.cfg)
	s.mu.Unlock()
	out := map[string]interface{}{"state": st, "tracker": name, "libs": libs, "root": mediaRoot(libs)}
	if r.URL.Query().Get("state") == "" {
		out["items"] = s.pantry.Items()
	}
	writeJSON(w, http.StatusOK, out)
}

// mediaRoot : le dossier qui contient les bibliothèques, pour proposer ses
// sous-dossiers (BARATIE_MEDIA, sinon le parent des bibliothèques réglées,
// sinon /data).
func mediaRoot(libs []string) string {
	if d := strings.TrimSpace(os.Getenv("BARATIE_MEDIA")); d != "" {
		return d
	}
	for _, l := range append(append([]string(nil), libs...), pantry.DefaultLibs...) {
		if d := filepath.Dir(filepath.Clean(l)); d != "." && d != "/" {
			return d
		}
	}
	return "/data"
}

// pantryLibs : les bibliothèques scannées, choisies dans l'onglet.
func (s *Server) pantryLibs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Libs []string `json:"libs"`
	}
	if !decode(w, r, 1<<16, &in) {
		return
	}
	var libs []string
	seen := map[string]bool{}
	for _, l := range in.Libs {
		l = filepath.Clean(strings.TrimSpace(l))
		if l == "." || l == "" || seen[l] {
			continue
		}
		if !filepath.IsAbs(l) {
			fail(w, http.StatusBadRequest, errors.New("« "+l+" » : chemin complet attendu (/data/media/…)"))
			return
		}
		seen[l] = true
		libs = append(libs, l)
	}
	if len(libs) == 0 {
		fail(w, http.StatusBadRequest, errors.New("choisis au moins une bibliothèque"))
		return
	}
	if err := s.updateConfig(func(c *config.Config) { c.PantryLibs = libs }); err != nil {
		fail(w, http.StatusInternalServerError, errors.New("bibliothèques non enregistrées : "+err.Error()))
		return
	}
	s.pantryList(w, r)
}

func (s *Server) pantryScan(w http.ResponseWriter, r *http.Request) {
	if err := s.pantry.Scan(); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"state": s.pantry.State()})
}

func (s *Server) pantryPause(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if !decode(w, r, 1<<10, &in) {
		return
	}
	s.pantry.Pause(in.On)
	writeJSON(w, http.StatusOK, map[string]interface{}{"state": s.pantry.State()})
}

// pantryMovie : le film TMDB choisi à la main (0 : l'oublier).
func (s *Server) pantryMovie(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TMDB int `json:"tmdb"`
	}
	if !decode(w, r, 1<<10, &in) {
		return
	}
	mv := tmdb.Movie{ID: in.TMDB}
	if in.TMDB > 0 {
		c := s.tmdbClient(w)
		if c == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		m, err := c.Details(ctx, in.TMDB)
		cancel()
		if err != nil {
			fail(w, http.StatusBadGateway, err)
			return
		}
		mv = *m
	}
	if err := s.pantry.SetMovie(r.PathValue("id"), mv); err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	it, _ := s.pantry.Get(r.PathValue("id"))
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) pantryIgnore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if !decode(w, r, 1<<10, &in) {
		return
	}
	if err := s.pantry.Ignore(r.PathValue("id"), in.On); err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	it, _ := s.pantry.Get(r.PathValue("id"))
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) pantryRecheck(w http.ResponseWriter, r *http.Request) {
	if err := s.pantry.Recheck(r.PathValue("id")); err != nil {
		fail(w, http.StatusNotFound, errors.New("film inconnu"))
		return
	}
	it, _ := s.pantry.Get(r.PathValue("id"))
	writeJSON(w, http.StatusOK, it)
}
