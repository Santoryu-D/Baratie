// Package musicbrainz cherche un album dans MusicBrainz (base ouverte de la
// musique) et dit s'il correspond aux pistes d'une release. D3X l'interroge
// directement, au rythme demandé par MusicBrainz (une requête par seconde),
// avec seulement l'artiste, l'album ou l'identifiant cherché.
package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Base de l'API (remplacée dans les tests).
var Base = "https://musicbrainz.org/ws/2"

// Release : une édition d'un album.
type Release struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Artist       string  `json:"artist"`
	Date         string  `json:"date,omitempty"`
	Country      string  `json:"country,omitempty"`
	Label        string  `json:"label,omitempty"`
	Catalog      string  `json:"catalog,omitempty"`
	Barcode      string  `json:"barcode,omitempty"`
	Format       string  `json:"format,omitempty"` // CD, Digital Media, 2×Vinyl…
	TrackCount   int     `json:"trackCount"`
	Status       string  `json:"status,omitempty"`
	Group        string  `json:"group,omitempty"` // release-group : l'œuvre, toutes éditions
	GroupType    string  `json:"groupType,omitempty"`
	Score        int     `json:"score,omitempty"` // pertinence de la recherche (0-100)
	Tracks       []Track `json:"tracks,omitempty"`
	CoverArchive bool    `json:"coverArchive,omitempty"` // pochette dans la Cover Art Archive
}

// Track : une piste de l'édition.
type Track struct {
	Disc     int     `json:"disc"`
	Position int     `json:"position"`
	Title    string  `json:"title"`
	Length   float64 `json:"length,omitempty"` // secondes
}

// Year : l'année de l'édition.
func (r Release) Year() string {
	if len(r.Date) >= 4 {
		return r.Date[:4]
	}
	return ""
}

// URL : la page de l'édition.
func (r Release) URL() string { return "https://musicbrainz.org/release/" + r.ID }

// Client : appels à MusicBrainz, une requête par seconde au plus.
type Client struct {
	hc  *http.Client
	ua  string
	gap time.Duration // écart entre deux requêtes

	mu   sync.Mutex
	next time.Time
	// Éditions déjà lues (une lecture par édition et par session).
	cache map[string]*Release
}

// New prépare un client ; MusicBrainz demande un User-Agent qui nomme
// l'application.
func New(version string) *Client {
	return &Client{hc: &http.Client{Timeout: 20 * time.Second}, ua: "D3X/" + version + " ( https://v3x.club )", gap: 1100 * time.Millisecond, cache: map[string]*Release{}}
}

// wait respecte le rythme d'une requête par seconde.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	at := c.next
	if at.Before(now) {
		at = now
	}
	c.next = at.Add(c.gap)
	c.mu.Unlock()
	select {
	case <-time.After(time.Until(at)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out interface{}) error {
	q.Set("fmt", "json")
	for attempt := 0; attempt < 3; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, Base+path+"?"+q.Encode(), nil)
		req.Header.Set("User-Agent", c.ua)
		req.Header.Set("Accept", "application/json")
		resp, err := c.hc.Do(req)
		if err != nil {
			return fmt.Errorf("MusicBrainz injoignable : %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests:
			// Trop de requêtes : MusicBrainz demande de ralentir.
			c.mu.Lock()
			c.next = time.Now().Add(3 * c.gap)
			c.mu.Unlock()
			continue
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode >= 300:
			return fmt.Errorf("MusicBrainz a répondu %d", resp.StatusCode)
		}
		return json.Unmarshal(body, out)
	}
	return errors.New("MusicBrainz est surchargé : réessaie dans un moment")
}

// ErrNotFound : identifiant inconnu de MusicBrainz.
var ErrNotFound = errors.New("édition inconnue de MusicBrainz")

// ---------- réponses de l'API ----------

type apiCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
}

type apiRelease struct {
	ID         string      `json:"id"`
	Score      int         `json:"score"`
	Title      string      `json:"title"`
	Status     string      `json:"status"`
	Date       string      `json:"date"`
	Country    string      `json:"country"`
	Barcode    string      `json:"barcode"`
	TrackCount int         `json:"track-count"`
	Credit     []apiCredit `json:"artist-credit"`
	Group      struct {
		ID          string `json:"id"`
		PrimaryType string `json:"primary-type"`
	} `json:"release-group"`
	LabelInfo []struct {
		Catalog string `json:"catalog-number"`
		Label   *struct {
			Name string `json:"name"`
		} `json:"label"`
	} `json:"label-info"`
	Media []struct {
		Format     string `json:"format"`
		Position   int    `json:"position"`
		TrackCount int    `json:"track-count"`
		Tracks     []struct {
			Position int    `json:"position"`
			Title    string `json:"title"`
			Length   *int64 `json:"length"`
		} `json:"tracks"`
	} `json:"media"`
	CoverArt struct {
		Front bool `json:"front"`
	} `json:"cover-art-archive"`
}

func (a apiRelease) release() Release {
	r := Release{ID: a.ID, Title: a.Title, Date: a.Date, Country: a.Country, Barcode: a.Barcode, Status: a.Status, Group: a.Group.ID, GroupType: a.Group.PrimaryType, Score: a.Score, CoverArchive: a.CoverArt.Front}
	var b strings.Builder
	for _, c := range a.Credit {
		b.WriteString(c.Name + c.JoinPhrase)
	}
	r.Artist = strings.TrimSpace(b.String())
	for _, l := range a.LabelInfo {
		if r.Label == "" && l.Label != nil {
			r.Label = l.Label.Name
		}
		if r.Catalog == "" && l.Catalog != "" && !strings.EqualFold(l.Catalog, "[none]") {
			r.Catalog = l.Catalog
		}
	}
	formats := map[string]int{}
	var order []string
	n := 0
	for _, m := range a.Media {
		n += m.TrackCount
		f := m.Format
		if f == "" {
			f = "?"
		}
		if formats[f] == 0 {
			order = append(order, f)
		}
		formats[f]++
		for _, t := range m.Tracks {
			tr := Track{Disc: m.Position, Position: t.Position, Title: t.Title}
			if t.Length != nil {
				tr.Length = float64(*t.Length) / 1000
			}
			r.Tracks = append(r.Tracks, tr)
		}
	}
	var parts []string
	for _, f := range order {
		if formats[f] > 1 {
			parts = append(parts, fmt.Sprintf("%d×%s", formats[f], f))
		} else {
			parts = append(parts, f)
		}
	}
	r.Format = strings.Join(parts, " + ")
	r.TrackCount = a.TrackCount
	if n > 0 {
		r.TrackCount = n
	}
	return r
}

// ---------- recherche et lecture ----------

// lucene échappe un texte pour la recherche de MusicBrainz.
func lucene(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\/`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Search cherche les éditions d'un album. Avec artiste et album séparés, la
// recherche porte sur chacun ; sinon sur le texte libre.
func (c *Client) Search(ctx context.Context, artist, album string, limit int) ([]Release, error) {
	artist, album = strings.TrimSpace(artist), strings.TrimSpace(album)
	var q string
	switch {
	case artist != "" && album != "":
		q = fmt.Sprintf(`release:"%s" AND artist:"%s"`, lucene(album), lucene(artist))
	case album != "":
		q = fmt.Sprintf(`release:(%s) OR artist:(%s)`, lucene(album), lucene(album))
	default:
		return nil, errors.New("rien à chercher")
	}
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	var out struct {
		Releases []apiRelease `json:"releases"`
	}
	if err := c.get(ctx, "/release", url.Values{"query": {q}, "limit": {fmt.Sprint(limit)}}, &out); err != nil {
		return nil, err
	}
	list := make([]Release, 0, len(out.Releases))
	for _, a := range out.Releases {
		r := a.release()
		r.Tracks = nil // la recherche ne donne pas les pistes
		list = append(list, r)
	}
	return list, nil
}

// Lookup lit une édition avec ses pistes et leurs durées.
func (c *Client) Lookup(ctx context.Context, id string) (*Release, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) != 36 {
		return nil, ErrNotFound
	}
	c.mu.Lock()
	if r := c.cache[id]; r != nil {
		c.mu.Unlock()
		return r, nil
	}
	c.mu.Unlock()
	var a apiRelease
	if err := c.get(ctx, "/release/"+url.PathEscape(id), url.Values{"inc": {"recordings artist-credits labels release-groups"}}, &a); err != nil {
		return nil, err
	}
	r := a.release()
	c.mu.Lock()
	if len(c.cache) > 2000 {
		c.cache = map[string]*Release{}
	}
	c.cache[id] = &r
	c.mu.Unlock()
	return &r, nil
}
