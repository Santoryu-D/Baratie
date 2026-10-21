// Package discogs : recherche d'un album sur Discogs et sa pochette, avec le
// jeton personnel du membre (Discogs ▸ Paramètres ▸ Développeurs). Sert quand
// MusicBrainz n'a pas de pochette (CoprinusComatus, 21/10/26).
package discogs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Base : l'API de Discogs (remplacée dans les tests).
var Base = "https://api.discogs.com"

// Client : appels à Discogs avec le jeton du membre.
type Client struct {
	hc    *http.Client
	ua    string
	token string
}

func New(version, token string) *Client {
	return &Client{hc: &http.Client{Timeout: 20 * time.Second}, ua: "D3X/" + version + " +https://github.com/V3x0rV3x/D3X", token: strings.TrimSpace(token)}
}

// Result : une édition trouvée.
type Result struct {
	ID      int64    `json:"id"`
	Title   string   `json:"title"` // « Artiste - Album »
	Year    string   `json:"year,omitempty"`
	Country string   `json:"country,omitempty"`
	Format  []string `json:"format,omitempty"`
	Label   []string `json:"label,omitempty"`
	Thumb   string   `json:"thumb,omitempty"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out interface{}) error {
	u := Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	// Jeton en en-tête, jamais dans l'adresse.
	req.Header.Set("Authorization", "Discogs token="+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("Discogs injoignable : %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("Discogs refuse le jeton : vérifie-le dans Réglages (Discogs ▸ Paramètres ▸ Développeurs)")
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("Discogs limite les appels : réessaie dans une minute")
	case resp.StatusCode == http.StatusNotFound:
		return errors.New("édition introuvable sur Discogs")
	case resp.StatusCode >= 300:
		return fmt.Errorf("Discogs a répondu HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("réponse de Discogs illisible")
	}
	return nil
}

// Search : éditions (releases) qui répondent à q, les 12 premières.
func (c *Client) Search(ctx context.Context, q string) ([]Result, error) {
	if c.token == "" {
		return nil, errors.New("ajoute ton jeton Discogs dans Réglages")
	}
	var res struct {
		Results []struct {
			ID      int64    `json:"id"`
			Title   string   `json:"title"`
			Year    string   `json:"year"`
			Country string   `json:"country"`
			Format  []string `json:"format"`
			Label   []string `json:"label"`
			Thumb   string   `json:"thumb"`
		} `json:"results"`
	}
	v := url.Values{"q": {q}, "type": {"release"}, "per_page": {"12"}}
	if err := c.get(ctx, "/database/search", v, &res); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(res.Results))
	for _, r := range res.Results {
		x := Result{ID: r.ID, Title: r.Title, Year: r.Year, Country: r.Country, Format: r.Format, Label: dedupe(r.Label)}
		if imageHost(r.Thumb) {
			x.Thumb = r.Thumb
		}
		out = append(out, x)
	}
	return out, nil
}

// CoverURL : la pochette principale d'une édition, en grand.
func (c *Client) CoverURL(ctx context.Context, id int64) (string, error) {
	if c.token == "" {
		return "", errors.New("ajoute ton jeton Discogs dans Réglages")
	}
	var rel struct {
		Images []struct {
			Type string `json:"type"`
			URI  string `json:"uri"`
		} `json:"images"`
	}
	if err := c.get(ctx, "/releases/"+strconv.FormatInt(id, 10), nil, &rel); err != nil {
		return "", err
	}
	best := ""
	for _, im := range rel.Images {
		if !imageHost(im.URI) {
			continue
		}
		if im.Type == "primary" {
			return im.URI, nil
		}
		if best == "" {
			best = im.URI
		}
	}
	if best == "" {
		return "", errors.New("pas de pochette pour cette édition sur Discogs")
	}
	return best, nil
}

// Download : l'image d'une adresse rendue par Discogs (ses serveurs d'images
// seulement, 16 Mio au plus).
func (c *Client) Download(ctx context.Context, u string) ([]byte, error) {
	if !imageHost(u) {
		return nil, errors.New("adresse d'image inattendue")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("User-Agent", c.ua)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pochette injoignable : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pochette : HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 16<<20 {
		return nil, errors.New("pochette trop lourde (16 Mio au plus)")
	}
	return b, nil
}

// imageHost : une image servie par Discogs (en https).
func imageHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "i.discogs.com" || h == "img.discogs.com" || strings.HasSuffix(h, ".discogs.com")
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
