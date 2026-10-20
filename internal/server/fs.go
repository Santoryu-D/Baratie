package server

import (
	"context"
	"net/http"
	"time"
)

// browse liste un dossier de ce PC ou de la seedbox (?source=ssh) pour
// choisir une release. Sans chemin : les points de départ.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	src, err := s.source(r.URL.Query().Get("source"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	exclude := s.cfg.ExcludeList()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	l, err := src.Browse(ctx, r.URL.Query().Get("path"), exclude)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}
