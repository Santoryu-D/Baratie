package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/crossseed"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
)

// Médiathèque : les films et séries rangés, retrouvés sur V3X
// (voir crossseed/library.go).

func (s *Server) routesLibrary() {
	s.engine.SetLibraryIO(libIO{s})
	s.mux.HandleFunc("GET /ui/library", s.libraryGet)
	s.mux.HandleFunc("POST /ui/library/folders", s.libraryFolders)
	s.mux.HandleFunc("POST /ui/library/scan", s.libraryScan)
	s.mux.HandleFunc("POST /ui/library/stop", func(w http.ResponseWriter, r *http.Request) {
		s.engine.StopLibrary()
		writeJSON(w, http.StatusOK, s.engine.Library())
	})
	s.mux.HandleFunc("POST /ui/library/apply", s.libraryApply)
	s.mux.HandleFunc("POST /ui/library/client", s.libraryClient)
}

// libIO : les fichiers de la médiathèque sur ce PC ou sur la seedbox.
type libIO struct{ s *Server }

func (l libIO) Files(ctx context.Context, src, dir string) ([]crossseed.LibFile, error) {
	so, err := l.s.source(src)
	if err != nil {
		return nil, err
	}
	l.s.mu.Lock()
	exclude := l.s.cfg.ExcludeList()
	l.s.mu.Unlock()
	p, err := so.Scan(ctx, dir, exclude)
	if err != nil {
		return nil, err
	}
	out := make([]crossseed.LibFile, 0, len(p.Files))
	for _, f := range p.Files {
		out = append(out, crossseed.LibFile{Path: f.Abs, Size: f.Size})
	}
	return out, nil
}

func (l libIO) ReadAt(ctx context.Context, src, path string, off, n int64) ([]byte, error) {
	so, err := l.s.source(src)
	if err != nil {
		return nil, err
	}
	return so.ReadAt(ctx, path, off, n)
}

// ClientPath : sur la seedbox, le client voit les mêmes chemins ; sur ce PC,
// par les dossiers du client qui reçoit l'ajout.
func (l libIO) ClientPath(src, path string, rules []pathmap.Rule) string {
	if src == "ssh" {
		return path
	}
	return pathmap.ToClient(path, rules)
}

// libTarget : le client qui reçoit les ajouts de la médiathèque (le premier
// si aucun n'est choisi ou si le choix n'existe plus ; verrou tenu).
func (s *Server) libTarget() *config.ClientConfig {
	if cc := s.cfg.Client(s.cfg.LibraryClient); cc != nil {
		return cc
	}
	if len(s.cfg.Clients) > 0 {
		return &s.cfg.Clients[0]
	}
	return nil
}

func (s *Server) libraryGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	folders := append([]config.LibFolder{}, s.cfg.Library...)
	on, target := s.cfg.Links.On, ""
	links := false
	if cc := s.libTarget(); cc != nil {
		target, links = cc.ID, on && strings.TrimSpace(cc.LinksDir) != ""
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state":   s.engine.Library(),
		"rows":    s.engine.LibraryRows(),
		"folders": folders,
		"links":   links,
		"client":  target,
	})
}

// libraryClient : le client qui reçoit les ajouts de la médiathèque.
func (s *Server) libraryClient(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Client string `json:"client"`
	}
	if !decode(w, r, 1<<12, &in) {
		return
	}
	s.mu.Lock()
	known := s.cfg.Client(in.Client) != nil
	s.mu.Unlock()
	if !known {
		fail(w, http.StatusBadRequest, errors.New("client inconnu"))
		return
	}
	if err := s.updateConfig(func(c *config.Config) { c.LibraryClient = in.Client }); err != nil {
		fail(w, http.StatusInternalServerError, errors.New("choix non enregistré : "+err.Error()))
		return
	}
	s.libraryGet(w, r)
}

func (s *Server) libraryFolders(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Folders []config.LibFolder `json:"folders"`
	}
	if !decode(w, r, 1<<16, &in) {
		return
	}
	var out []config.LibFolder
	seen := map[string]bool{}
	for _, f := range in.Folders {
		f.Path = strings.TrimSpace(f.Path)
		if f.Source != "ssh" {
			f.Source = "local"
		}
		k := f.Source + "|" + strings.ToLower(f.Path)
		if f.Path == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
	}
	if len(out) > 30 {
		fail(w, http.StatusBadRequest, errors.New("30 dossiers au plus"))
		return
	}
	s.mu.Lock()
	c := *s.cfg
	c.Library = out
	s.cfg = &c
	s.mu.Unlock()
	if err := config.Save(&c); err != nil {
		fail(w, http.StatusInternalServerError, errors.New("dossiers non enregistrés : "+err.Error()))
		return
	}
	s.libraryGet(w, r)
}

func (s *Server) libraryScan(w http.ResponseWriter, r *http.Request) {
	if err := s.ready(); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	folders := append([]config.LibFolder{}, s.cfg.Library...)
	s.mu.Unlock()
	if err := s.engine.StartLibrary(folders); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	time.Sleep(50 * time.Millisecond)
	writeJSON(w, http.StatusOK, s.engine.Library())
}

func (s *Server) libraryApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys []string `json:"keys"`
	}
	if !decode(w, r, 1<<20, &in) || len(in.Keys) == 0 {
		if len(in.Keys) == 0 {
			fail(w, http.StatusBadRequest, errors.New("aucune release choisie"))
		}
		return
	}
	n, err := s.engine.ApplyLibrary(in.Keys)
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"queued": n})
}
