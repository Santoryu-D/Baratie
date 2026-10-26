package pantry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"baratie/internal/fiche"
)

// Radarr : lecture seule de la liste des films (GET /api/v3/movie).
type Radarr struct {
	URL string
	Key string
	hc  *http.Client
}

// NewRadarr crée le client.
func NewRadarr(u, key string) *Radarr {
	return &Radarr{URL: strings.TrimRight(strings.TrimSpace(u), "/"), Key: strings.TrimSpace(key), hc: &http.Client{Timeout: 60 * time.Second}}
}

// RadarrIndex : l'ID TMDB par fichier et par dossier de film.
type RadarrIndex struct {
	ByFile map[string]int
	ByDir  map[string]int
	// Info : langue originale (code ISO 639-1) et animation, par ID TMDB.
	Info map[int]MovieInfo
}

// MovieInfo : ce que Radarr dit d'un film.
type MovieInfo struct {
	Lang string
	Anim bool
}

// Lookup : l'ID TMDB d'un fichier (par son chemin, sinon son dossier).
func (x *RadarrIndex) Lookup(path string) int {
	if x == nil {
		return 0
	}
	if id := x.ByFile[filepath.Clean(path)]; id > 0 {
		return id
	}
	for d := filepath.Dir(path); d != "/" && d != "." && d != filepath.Dir(d); d = filepath.Dir(d) {
		if id := x.ByDir[d]; id > 0 {
			return id
		}
	}
	return 0
}

// Index lit tous les films de Radarr.
func (r *Radarr) Index(ctx context.Context) (*RadarrIndex, error) {
	if r.URL == "" || r.Key == "" {
		return nil, errors.New("Radarr non réglé")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+"/api/v3/movie", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", r.Key)
	req.Header.Set("Accept", "application/json")
	resp, err := r.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("Radarr injoignable : %v", ue.Err)
		}
		return nil, errors.New("Radarr injoignable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("Radarr refuse la clé API")
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Radarr : HTTP %d", resp.StatusCode)
	}
	var list []struct {
		TmdbID   int      `json:"tmdbId"`
		Path     string   `json:"path"`
		Genres   []string `json:"genres"`
		OrigLang *struct {
			Name string `json:"name"`
		} `json:"originalLanguage"`
		MovieFile *struct {
			Path string `json:"path"`
		} `json:"movieFile"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(&list); err != nil {
		return nil, errors.New("Radarr : réponse illisible")
	}
	x := &RadarrIndex{ByFile: map[string]int{}, ByDir: map[string]int{}, Info: map[int]MovieInfo{}}
	for _, m := range list {
		if m.TmdbID <= 0 {
			continue
		}
		var inf MovieInfo
		if m.OrigLang != nil {
			inf.Lang = fiche.LangCode(m.OrigLang.Name)
		}
		for _, g := range m.Genres {
			inf.Anim = inf.Anim || strings.EqualFold(g, "Animation")
		}
		x.Info[m.TmdbID] = inf
		if m.Path != "" {
			x.ByDir[filepath.Clean(m.Path)] = m.TmdbID
		}
		if m.MovieFile != nil && m.MovieFile.Path != "" {
			x.ByFile[filepath.Clean(m.MovieFile.Path)] = m.TmdbID
		}
	}
	return x, nil
}
