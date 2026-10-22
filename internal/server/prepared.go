package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/journal"
)

// Fiches gardées pour le lot (CoprinusComatus, 21/10/26) : une release du lot
// ouverte par « Revoir » et corrigée dans Publier peut être gardée telle
// quelle ; le lot la publie ensuite avec CETTE fiche, sans la refaire. Rangées
// par release (même clé que le journal), sur ce PC seulement, les plus
// récentes d'abord.

const preparedFile = "lot-fiches.json"
const preparedMax = 300

type prepared struct {
	Name    string            `json:"name"`
	Summary string            `json:"summary,omitempty"`
	Fields  map[string]string `json:"fields"`
	At      time.Time         `json:"at"`
}

var preparedMu sync.Mutex

func loadPrepared() map[string]prepared {
	out := map[string]prepared{}
	p, err := config.Path(preparedFile)
	if err != nil {
		return out
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func savePrepared(m map[string]prepared) error {
	if len(m) > preparedMax {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return m[keys[i]].At.After(m[keys[j]].At) })
		for _, k := range keys[preparedMax:] {
			delete(m, k)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(preparedFile, b)
}

// dropPrepared oublie la fiche gardée d'une release (remise en attente,
// ignorée, publiée).
func dropPrepared(key string) {
	preparedMu.Lock()
	defer preparedMu.Unlock()
	m := loadPrepared()
	if _, ok := m[key]; ok {
		delete(m, key)
		_ = savePrepared(m)
	}
}

func (s *Server) routesPrepared() {
	s.mux.HandleFunc("POST /ui/batch/prepare", s.batchPrepare)
	s.mux.HandleFunc("GET /ui/batch/prepared", s.batchPrepared)
	s.mux.HandleFunc("POST /ui/batch/prepared/drop", s.batchPreparedDrop)
}

// Champs que Publier envoie à V3X : rien d'autre n'est gardé.
var preparedKeys = map[string]bool{
	"categoryId": true, "description": true, "descriptionFormat": true, "title": true, "releaseName": true,
	"nfo": true, "language": true, "anonymous": true, "exclusive": true, "season": true, "tmdbUrl": true,
	"creator": true, "noTmdb": true, "virusTotalUrl": true, "teamRelease": true,
	// Jaquette d'un jeu (1.1.21) : sans elle, le lot publiait le jeu sans affiche.
	"posterUrl": true,
}

func (s *Server) batchPrepare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string            `json:"path"`
		Source  string            `json:"source"`
		Name    string            `json:"name"`
		Summary string            `json:"summary"`
		Fields  map[string]string `json:"fields"`
	}
	if !decode(w, r, 4<<20, &in) {
		return
	}
	if strings.TrimSpace(in.Path) == "" || strings.TrimSpace(in.Fields["releaseName"]) == "" || strings.TrimSpace(in.Fields["categoryId"]) == "" {
		fail(w, http.StatusBadRequest, errors.New("fiche incomplète : il faut au moins le nom et la catégorie"))
		return
	}
	src, err := s.source(in.Source)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	f := map[string]string{}
	for k, v := range in.Fields {
		if preparedKeys[k] {
			f[k] = v
		}
	}
	summary := in.Summary
	if utf8.RuneCountInString(summary) > 300 {
		summary = string([]rune(summary)[:300])
	}
	key := src.Key(in.Path)
	preparedMu.Lock()
	m := loadPrepared()
	m[key] = prepared{Name: in.Name, Summary: summary, Fields: f, At: time.Now()}
	err = savePrepared(m)
	preparedMu.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, errors.New("fiche non gardée : "+err.Error()))
		return
	}
	journal.Add(journal.Event{Kind: "batch_ready", Name: in.Name, Path: key, Detail: summary})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) batchPrepared(w http.ResponseWriter, r *http.Request) {
	src, err := s.source(r.URL.Query().Get("source"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	preparedMu.Lock()
	p, ok := loadPrepared()[src.Key(r.URL.Query().Get("path"))]
	preparedMu.Unlock()
	if !ok {
		fail(w, http.StatusNotFound, errors.New("fiche gardée introuvable : rouvre la release avec « Revoir »"))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) batchPreparedDrop(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	if !decode(w, r, 1<<16, &in) {
		return
	}
	src, err := s.source(in.Source)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	dropPrepared(src.Key(in.Path))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
