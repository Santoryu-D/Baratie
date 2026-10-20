package v3x

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// SeedStatus : ce que le site attend du seed du membre (hit and run à
// régler) et les relais ouverts (releases rares à reprendre, avec prime).
type SeedStatus struct {
	HnR struct {
		Enabled              bool      `json:"enabled"`
		MinSeedHours         int       `json:"minSeedHours"`
		WindowDays           int       `json:"windowDays"`
		MaxActive            int       `json:"maxActive"`
		UploaderMinSeedHours int       `json:"uploaderMinSeedHours"`
		UploaderSafeSeeders  int       `json:"uploaderSafeSeeders"`
		Exempt               bool      `json:"exempt"`
		Active               int       `json:"active"`
		Items                []HnRItem `json:"items"`
	} `json:"hnr"`
	Relays struct {
		Prime        int     `json:"prime"`
		MaxPaid      int     `json:"maxPaid"`
		SustainHours int     `json:"sustainHours"`
		Items        []Relay `json:"items"`
	} `json:"relays"`
}

// HnRItem : une release à seeder encore pour éviter un hit and run.
type HnRItem struct {
	InfoHash        string    `json:"infoHash"`
	TorrentID       string    `json:"torrentId"`
	Name            string    `json:"name"`
	SeedSeconds     int64     `json:"seedSeconds"`
	RequiredSeconds int64     `json:"requiredSeconds"`
	Deadline        time.Time `json:"deadline"`
	HnR             bool      `json:"hnr"` // hit and run déjà marqué
	Uploader        bool      `json:"uploader"`
	Seeders         int       `json:"seeders"`
	SeedingNow      bool      `json:"seedingNow"` // vu en seed par le tracker
}

// Relay : release rare lâchée par un membre ; prime au repreneur.
type Relay struct {
	InfoHash  string    `json:"infoHash"`
	TorrentID string    `json:"torrentId"`
	Name      string    `json:"name"`
	Seeders   int       `json:"seeders"`
	Until     time.Time `json:"until"`
}

// SeedStatus lit l'état du seed du membre.
func (c *Client) SeedStatus(ctx context.Context) (*SeedStatus, error) {
	var out SeedStatus
	if err := c.doJSON(ctx, &Retry{}, http.MethodGet, "/api/d3x/seed-status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Request : une demande ouverte sur V3X.
type Request struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Year         string `json:"year"`
	MediaType    string `json:"mediaType"` // movie, tv
	TmdbID       int    `json:"tmdbId"`
	Quality      string `json:"quality"`
	Sources      string `json:"sources"`
	Language     string `json:"language"`
	CategorySlug string `json:"categorySlug"`
	Reward       int    `json:"reward"` // jetons pour qui la remplit
	Mine         bool   `json:"mine"`
}

// Requests liste les demandes ouvertes.
func (c *Client) Requests(ctx context.Context) ([]Request, error) {
	var out struct {
		Requests []Request `json:"requests"`
	}
	if err := c.doJSON(ctx, &Retry{}, http.MethodGet, "/api/d3x/requests", nil, &out); err != nil {
		return nil, err
	}
	return out.Requests, nil
}

// Propose propose une release publiée pour une demande.
func (c *Client) Propose(ctx context.Context, requestID, torrentID string) error {
	return c.doJSON(ctx, nil, http.MethodPost, "/api/d3x/requests/"+url.PathEscape(requestID)+"/propose", map[string]string{"torrentId": torrentID}, nil)
}

// FileMatch : un torrent V3X qui contient un fichier de la taille cherchée.
type FileMatch struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	InfoHash  string `json:"infoHash"`
	Size      int64  `json:"size"`
	FileCount int    `json:"fileCount"`
	Seeders   int    `json:"seeders"`
	Leechers  int    `json:"leechers"`
}

// MatchFilesSize : au plus 1000 tailles par appel.
const MatchFilesSize = 1000

// MatchFiles cherche les torrents qui contiennent un fichier de ces tailles
// (gros fichiers seulement : ni nom ni chemin ne sont envoyés).
func (c *Client) MatchFiles(ctx context.Context, r *Retry, sizes []int64) (map[int64][]FileMatch, error) {
	var out struct {
		Matches map[string][]FileMatch `json:"matches"`
	}
	if err := c.doJSON(ctx, r, http.MethodPost, "/api/d3x/match-files", map[string]interface{}{"sizes": sizes}, &out); err != nil {
		return nil, err
	}
	res := make(map[int64][]FileMatch, len(out.Matches))
	for k, v := range out.Matches {
		if n, err := strconv.ParseInt(k, 10, 64); err == nil {
			res[n] = v
		}
	}
	return res, nil
}

// TeamPolicy : une team dont le réupload est interdit (« forbidden ») ou soumis
// à son accord (« ask »). Allowed : le membre en fait partie ou a leur accord.
type TeamPolicy struct {
	Name    string   `json:"name"`
	Tags    []string `json:"tags"`
	Policy  string   `json:"policy"`
	Allowed bool     `json:"allowed"`
}

// TeamPolicies liste les teams dont le réupload n'est pas libre.
func (c *Client) TeamPolicies(ctx context.Context) ([]TeamPolicy, error) {
	var out struct {
		Teams []TeamPolicy `json:"teams"`
	}
	if err := c.doJSON(ctx, &Retry{}, http.MethodGet, "/api/d3x/team-policies", nil, &out); err != nil {
		return nil, err
	}
	return out.Teams, nil
}
