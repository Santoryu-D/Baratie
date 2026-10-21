package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/V3x0rV3x/D3X/internal/discogs"
)

// Pochette depuis Discogs (CoprinusComatus, 21/10/26) : recherche avec le jeton
// du membre, puis la pochette choisie est hébergée sur V3X avec sa clé, comme
// le bouton « Image du PC ».
func (s *Server) routesDiscogs() {
	s.mux.HandleFunc("GET /ui/discogs/search", s.discogsSearch)
	s.mux.HandleFunc("POST /ui/discogs/cover", s.discogsCover)
}

func (s *Server) discogsClient() (*discogs.Client, error) {
	s.mu.Lock()
	tok := s.cfg.DiscogsToken
	s.mu.Unlock()
	if tok == "" {
		return nil, errors.New("ajoute ton jeton Discogs dans Réglages")
	}
	return discogs.New(s.Version, tok), nil
}

func (s *Server) discogsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 200 {
		fail(w, http.StatusBadRequest, errors.New("écris l'artiste et l'album"))
		return
	}
	c, err := s.discogsClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := c.Search(ctx, q)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": res})
}

func (s *Server) discogsCover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID int64 `json:"id"`
	}
	if !decode(w, r, 1<<10, &in) {
		return
	}
	if in.ID <= 0 {
		fail(w, http.StatusBadRequest, errors.New("édition Discogs inconnue"))
		return
	}
	c, err := s.discogsClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	v, err := s.v3xClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	src, err := c.CoverURL(ctx, in.ID)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	img, err := c.Download(ctx, src)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if !strings.HasPrefix(http.DetectContentType(img), "image/") {
		fail(w, http.StatusBadGateway, errors.New("Discogs n'a pas renvoyé une image"))
		return
	}
	u, err := v.UploadImage(ctx, "discogs-"+strconv.FormatInt(in.ID, 10)+".jpg", img)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}
