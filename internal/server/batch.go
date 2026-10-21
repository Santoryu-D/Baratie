package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/V3x0rV3x/D3X/internal/journal"
	"github.com/V3x0rV3x/D3X/internal/releases"
)

// Mode lot : chaque entrée d'un dossier (sous-dossier ou fichier) est une
// release. La page mène le lot release par release, avec les mêmes routes
// que la vue Publier ; le serveur liste et garde les décisions.

type batchEntry struct {
	releases.Entry
	Last *journal.Event `json:"last,omitempty"` // dernière décision ou publication
}

func (s *Server) routesBatch() {
	s.mux.HandleFunc("GET /ui/batch/list", s.batchList)
	s.mux.HandleFunc("POST /ui/batch/decision", s.batchDecision)
	s.routesPrepared()
	s.routesDiscogs()
}

func (s *Server) batchList(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("path"))
	if dir == "" {
		fail(w, http.StatusBadRequest, errors.New("choisis un dossier"))
		return
	}
	src, err := s.source(r.URL.Query().Get("source"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	exclude := s.cfg.ExcludeList()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	list, err := src.List(ctx, dir, exclude)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	last := journal.LastByPath()
	out := make([]batchEntry, 0, len(list))
	for _, e := range list {
		be := batchEntry{Entry: e}
		if ev, ok := last[src.Key(e.Path)]; ok {
			ev := ev
			be.Last = &ev
		}
		out = append(out, be)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"path": dir, "entries": out})
}

// batchDecision garde la décision du lot pour une release (à revoir, simulée,
// ignorée, déjà présente) : elle revient à la prochaine liste.
func (s *Server) batchDecision(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path   string `json:"path"`
		Source string `json:"source"`
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Detail string `json:"detail"`
		ID     string `json:"id"`
	}
	if !decode(w, r, 1<<16, &in) {
		return
	}
	switch in.Kind {
	case "batch_review", "batch_simulated", "batch_ignored", "batch_unignored", "batch_duplicate", "batch_error", "batch_reset":
	default:
		fail(w, http.StatusBadRequest, errors.New("décision inconnue"))
		return
	}
	src, err := s.source(in.Source)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	key := src.Key(in.Path)
	// Remise en attente ou écartée : la fiche gardée pour le lot ne sert plus.
	if in.Kind == "batch_reset" || in.Kind == "batch_ignored" {
		dropPrepared(key)
	}
	journal.Add(journal.Event{Kind: in.Kind, Name: in.Name, Path: key, Detail: in.Detail, ID: in.ID})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
