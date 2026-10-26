package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"baratie/internal/config"
	"baratie/internal/pathmap"
)

func (s *Server) routesPublish() {
	s.mux.HandleFunc("POST /ui/pub/start", s.pubStart)
	s.mux.HandleFunc("GET /ui/pub/{id}", s.pubGet)
	s.mux.HandleFunc("POST /ui/pub/{id}/cancel", s.pubCancel)
	s.mux.HandleFunc("GET /ui/pub/{id}/file/{index}", s.pubFile)
	s.mux.HandleFunc("POST /ui/pub/{id}/clientdir", s.pubClientDir)
	s.mux.HandleFunc("POST /ui/pub/{id}/client", s.pubClient)
}

func (s *Server) pubStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, 1<<14, &in) {
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		fail(w, http.StatusBadRequest, errors.New("choisis un fichier ou un dossier"))
		return
	}
	j, err := s.pub.Start(in.Path)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.lastDir(j.Path)
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) pubGet(w http.ResponseWriter, r *http.Request) {
	j, ok := s.pub.Get(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("publication inconnue"))
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) pubCancel(w http.ResponseWriter, r *http.Request) {
	s.pub.Cancel(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// pubFile sert un morceau d'un fichier de la release (MediaInfo lit le fichier
// par morceaux, dans la page). Rien d'autre n'est lisible.
func (s *Server) pubFile(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		fail(w, http.StatusBadRequest, errors.New("fichier inconnu"))
		return
	}
	q := r.URL.Query()
	off, _ := strconv.ParseInt(q.Get("offset"), 10, 64)
	n, _ := strconv.ParseInt(q.Get("size"), 10, 64)
	b, size, err := s.pub.ReadFile(r.PathValue("id"), idx, off, n)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-File-Size", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// pubClientDir corrige le dossier où le client verra la release ; retenu pour
// les prochaines publications si demandé.
func (s *Server) pubClientDir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir      string `json:"dir"`
		Remember bool   `json:"remember"`
	}
	if !decode(w, r, 1<<14, &in) {
		return
	}
	rule, cid, err := s.pub.SetClientDir(r.PathValue("id"), in.Dir)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if in.Remember && rule != nil {
		err := s.updateConfig(func(c *config.Config) {
			cc := c.Client(cid)
			if cc == nil {
				return
			}
			rules := []pathmap.Rule{*rule}
			for _, x := range cc.PathMap {
				if x.From != rule.From {
					rules = append(rules, x)
				}
			}
			cc.PathMap = rules
		})
		if err != nil {
			fail(w, http.StatusInternalServerError, errors.New("dossier non retenu : "+err.Error()))
			return
		}
	}
	s.pubGet(w, r)
}

// pubClient : le client qui seedera la release.
func (s *Server) pubClient(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Client string `json:"client"`
	}
	if !decode(w, r, 1<<12, &in) {
		return
	}
	if err := s.pub.SetClient(r.PathValue("id"), in.Client); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.pubGet(w, r)
}
