package server

// Baratie : captures d'écran de la fiche (internal/shots) et hébergement sur
// imgbb (vérifié à travers wsrv.nl, le relais d'images d'UNIT3D). Prises seulement sur un clic, hébergées seulement sur un autre clic.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"baratie/internal/config"
	"baratie/internal/shots"
)

// imgbbFallbackKey : clé de la démo seulement (faux imgbb).
var imgbbFallbackKey string

var shotSets = struct {
	sync.Mutex
	m map[string]*shots.Set
}{m: map[string]*shots.Set{}}

func (s *Server) routesShots() {
	s.mux.HandleFunc("POST /ui/shots/{id}", s.shotsMake)
	s.mux.HandleFunc("GET /ui/shots/{id}", s.shotsGet)
	s.mux.HandleFunc("GET /ui/shots/{id}/{i}", s.shotsImage)
	s.mux.HandleFunc("POST /ui/shots/{id}/{i}/redo", s.shotsRedo)
	s.mux.HandleFunc("POST /ui/shots/{id}/host", s.shotsHost)
}

func shotsOut(id string, set *shots.Set) map[string]interface{} {
	list := []map[string]interface{}{}
	for _, sh := range set.Shots {
		list = append(list, map[string]interface{}{"index": sh.Index, "at": sh.At, "size": sh.Size, "error": sh.Error, "url": "/ui/shots/" + id + "/" + strconv.Itoa(sh.Index) + "?v=" + filepath.Base(sh.Path)})
	}
	return map[string]interface{}{"duration": set.Dur, "notes": set.Notes, "warn": set.Warn, "shots": list}
}

func (s *Server) shotsMake(w http.ResponseWriter, r *http.Request) {
	var in struct {
		N int `json:"n"`
	}
	if !decode(w, r, 1<<10, &in) {
		return
	}
	if in.N < 3 || in.N > 8 {
		in.N = 4
	}
	id := r.PathValue("id")
	file, err := s.pub.MainFile(id)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	tool, err := shots.Find()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	base, err := config.Dir()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	dir, err := os.MkdirTemp(base, "captures-")
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	set, err := tool.Make(ctx, file, dir, in.N)
	if err != nil {
		os.RemoveAll(dir)
		fail(w, http.StatusBadGateway, err)
		return
	}
	shotSets.Lock()
	if old := shotSets.m[id]; old != nil {
		old.Clean()
	}
	shotSets.m[id] = set
	shotSets.Unlock()
	writeJSON(w, http.StatusOK, shotsOut(id, set))
}

func getSet(id string) *shots.Set {
	shotSets.Lock()
	defer shotSets.Unlock()
	return shotSets.m[id]
}

func (s *Server) shotsGet(w http.ResponseWriter, r *http.Request) {
	set := getSet(r.PathValue("id"))
	if set == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"shots": []interface{}{}})
		return
	}
	writeJSON(w, http.StatusOK, shotsOut(r.PathValue("id"), set))
}

func (s *Server) shotsImage(w http.ResponseWriter, r *http.Request) {
	set := getSet(r.PathValue("id"))
	i, err := strconv.Atoi(r.PathValue("i"))
	if set == nil || err != nil || i < 0 || i >= len(set.Shots) || set.Shots[i].Error != "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	http.ServeFile(w, r, set.Shots[i].Path)
}

func (s *Server) shotsRedo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	set := getSet(id)
	i, _ := strconv.Atoi(r.PathValue("i"))
	if set == nil {
		fail(w, http.StatusNotFound, errors.New("aucune capture : génère-les d'abord"))
		return
	}
	tool, err := shots.Find()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	shotSets.Lock()
	err = tool.Redo(ctx, set, i)
	shotSets.Unlock()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, shotsOut(id, set))
}

// shotsHost envoie les captures sur imgbb (un clic du membre), vérifie que
// chacune passe le relais d'images d'UNIT3D (wsrv.nl) et rend le BBCode des
// vignettes ; les images de travail sont ensuite effacées.
func (s *Server) shotsHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	set := getSet(id)
	if set == nil {
		fail(w, http.StatusNotFound, errors.New("aucune capture : génère-les d'abord"))
		return
	}
	for _, sh := range set.Shots {
		if sh.Error != "" {
			fail(w, http.StatusBadRequest, errors.New("refais d'abord la capture "+strconv.Itoa(sh.Index+1)+" : "+sh.Error))
			return
		}
	}
	s.mu.Lock()
	key := s.cfg.ImgBBKey
	s.mu.Unlock()
	if key == "" {
		key = imgbbFallbackKey
	}
	var hosted []shots.Hosted
	warn := ""
	for _, sh := range set.Shots {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		h, err := shots.ImgBB(ctx, key, sh.Path)
		if err == nil {
			if cerr := shots.CheckRelay(ctx, h.Thumb); cerr == shots.ErrRelayDown {
				warn = "Vérification impossible : wsrv.nl (le relais d'images d'UNIT3D) ne répond pas depuis le NAS. Regarde la fiche après publication."
			} else if cerr != nil {
				err = errors.New("hébergée, mais " + cerr.Error() + " : elle serait cassée sur le tracker, rien n'est ajouté à la description")
			}
		}
		cancel()
		if err != nil {
			fail(w, http.StatusBadGateway, errors.New("capture "+strconv.Itoa(sh.Index+1)+" : "+err.Error()))
			return
		}
		hosted = append(hosted, h)
	}
	shotSets.Lock()
	delete(shotSets.m, id)
	shotSets.Unlock()
	set.Clean()
	writeJSON(w, http.StatusOK, map[string]interface{}{"hosted": hosted, "bbcode": shots.BBCode(hosted), "warn": warn})
}
