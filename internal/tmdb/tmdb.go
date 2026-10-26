// Package tmdb cherche les films sur TMDB avec la clé du membre (clé v3 ou
// jeton de lecture v4). La clé n'apparaît dans aucune erreur.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Base : l'API TMDB (changée par les tests).
var Base = "https://api.themoviedb.org/3"

// ImgBase : les affiches (changée par la démo).
var ImgBase = "https://image.tmdb.org/t/p/w185"

// Client parle à TMDB.
type Client struct {
	key string
	hc  *http.Client
}

// New crée le client.
func New(key string) *Client {
	return &Client{key: strings.TrimSpace(key), hc: &http.Client{Timeout: 20 * time.Second}}
}

// Movie : un film.
type Movie struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	Year          string `json:"year"`
	Overview      string `json:"overview,omitempty"`
	Poster        string `json:"poster,omitempty"`
	IMDB          int    `json:"imdb,omitempty"` // 1375666 pour tt1375666
	// TitleEN : le titre anglais (nom international, pour le titre de fiche).
	TitleEN string `json:"titleEN,omitempty"`
	// OrigLang : langue originale (ISO 639-1) ; Animation : genre animation.
	OrigLang  string `json:"origLang,omitempty"`
	Animation bool   `json:"animation,omitempty"`
}

type rawMovie struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate   string `json:"release_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
	IMDBID        string `json:"imdb_id"`
	OrigLang      string `json:"original_language"`
	GenreIDs      []int  `json:"genre_ids"`
	Genres        []struct {
		ID int `json:"id"`
	} `json:"genres"`
}

func (r rawMovie) movie() Movie {
	m := Movie{ID: r.ID, Title: r.Title, OriginalTitle: r.OriginalTitle, Overview: r.Overview, IMDB: IMDBNumber(r.IMDBID), OrigLang: r.OrigLang}
	// Genre 16 : Animation.
	for _, g := range r.GenreIDs {
		m.Animation = m.Animation || g == 16
	}
	for _, g := range r.Genres {
		m.Animation = m.Animation || g.ID == 16
	}
	if len(r.ReleaseDate) >= 4 {
		m.Year = r.ReleaseDate[:4]
	}
	if r.PosterPath != "" {
		m.Poster = ImgBase + r.PosterPath
	}
	return m
}

// IMDBNumber : « tt1375666 » → 1375666 (0 sinon).
func IMDBNumber(s string) int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "tt")
	n, _ := strconv.Atoi(s)
	return n
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out interface{}) error {
	return c.getLang(ctx, path, q, "fr-FR", out)
}

func (c *Client) getLang(ctx context.Context, path string, q url.Values, lang string, out interface{}) error {
	if c.key == "" {
		return errors.New("clé TMDB manquante : ajoute-la dans les réglages")
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("language", lang)
	v4 := strings.HasPrefix(c.key, "eyJ")
	if !v4 {
		q.Set("api_key", c.key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if v4 {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("TMDB injoignable : %v", ue.Err)
		}
		return errors.New("TMDB injoignable")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("TMDB refuse la clé : vérifie-la dans les réglages")
	case resp.StatusCode == http.StatusNotFound:
		return errors.New("film introuvable sur TMDB")
	case resp.StatusCode >= 300:
		return fmt.Errorf("TMDB : HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(data, out)
}

// Search cherche un film par titre (et année si connue ; sans résultat avec
// l'année, on réessaie sans).
func (c *Client) Search(ctx context.Context, query, year string) ([]Movie, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("titre vide")
	}
	var r struct {
		Results []rawMovie `json:"results"`
	}
	q := url.Values{"query": {query}, "include_adult": {"false"}}
	if year != "" {
		q.Set("year", year)
	}
	if err := c.get(ctx, "/search/movie", q, &r); err != nil {
		return nil, err
	}
	if len(r.Results) == 0 && year != "" {
		return c.Search(ctx, query, "")
	}
	out := []Movie{}
	for i, m := range r.Results {
		if i >= 12 {
			break
		}
		out = append(out, m.movie())
	}
	return out, nil
}

// Details : un film avec son ID IMDB.
func (c *Client) Details(ctx context.Context, id int) (*Movie, error) {
	var r rawMovie
	if err := c.get(ctx, "/movie/"+strconv.Itoa(id), nil, &r); err != nil {
		return nil, err
	}
	m := r.movie()
	// Titre anglais : le nom international (règle de nommage des trackers).
	var en rawMovie
	if err := c.getLang(ctx, "/movie/"+strconv.Itoa(id), nil, "en-US", &en); err == nil && en.Title != "" {
		m.TitleEN = en.Title
	}
	return &m, nil
}

var (
	tmdbRef = regexp.MustCompile(`themoviedb\.org/movie/(\d+)`)
	imdbRef = regexp.MustCompile(`\btt\d{5,10}\b`)
)

// Lookup : un lien TMDB ou IMDB, un numéro TMDB, ou « tt1375666 ».
func (c *Client) Lookup(ctx context.Context, ref string) ([]Movie, error) {
	ref = strings.TrimSpace(ref)
	if m := tmdbRef.FindStringSubmatch(ref); m != nil {
		ref = m[1]
	}
	if n, err := strconv.Atoi(ref); err == nil && n > 0 {
		m, err := c.Details(ctx, n)
		if err != nil {
			return nil, err
		}
		return []Movie{*m}, nil
	}
	if tt := imdbRef.FindString(ref); tt != "" {
		var r struct {
			Movies []rawMovie `json:"movie_results"`
		}
		if err := c.get(ctx, "/find/"+tt, url.Values{"external_source": {"imdb_id"}}, &r); err != nil {
			return nil, err
		}
		out := []Movie{}
		for _, m := range r.Movies {
			mv := m.movie()
			mv.IMDB = IMDBNumber(tt)
			out = append(out, mv)
		}
		return out, nil
	}
	return nil, errors.New("référence non reconnue : colle un lien TMDB ou IMDB")
}
