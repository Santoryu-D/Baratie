package v3x

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
)

// Ces routes demandent une clé avec le droit « Upload ». La fiche est bâtie
// par le site lui-même (le même générateur que sa page d'upload) : une fiche
// publiée avec D3X est identique à une fiche faite sur V3X.

// PublishContext lit le contexte du membre (équipe, catégories, langues,
// modèles de présentation), relayé tel quel à la page.
func (c *Client) PublishContext(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.doJSON(ctx, nil, http.MethodGet, "/api/d3x/context", nil, &raw)
	return raw, err
}

// TMDB relaie une des routes TMDB du site (search, lookup, details, show-kind).
func (c *Client) TMDB(ctx context.Context, kind string, q url.Values) (json.RawMessage, error) {
	switch kind {
	case "search", "lookup", "details", "show-kind":
	default:
		return nil, fmt.Errorf("route TMDB inconnue : %s", kind)
	}
	var raw json.RawMessage
	err := c.doJSON(ctx, nil, http.MethodGet, "/api/d3x/tmdb/"+kind+"?"+q.Encode(), nil, &raw)
	return raw, err
}

// FicheRequest : de quoi bâtir la fiche.
type FicheRequest struct {
	ReleaseName  string        `json:"releaseName"`
	MediaInfo    string        `json:"mediainfo"`
	TmdbRef      *string       `json:"tmdbRef,omitempty"`
	MbRef        *string       `json:"mbRef,omitempty"` // édition MusicBrainz (musique)
	Download     FicheDownload `json:"download"`
	CategorySlug *string       `json:"categorySlug,omitempty"`
	TemplateID   *string       `json:"templateId,omitempty"`
}

// FicheDownload décrit la release (encart « Téléchargement » de la présentation).
type FicheDownload struct {
	Name      string `json:"name"`
	TotalSize int64  `json:"totalSize"`
	FileCount int    `json:"fileCount"`
}

// Fiche demande au site la fiche proposée (nom, catégorie, présentation…).
func (c *Client) Fiche(ctx context.Context, req FicheRequest) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.doJSON(ctx, nil, http.MethodPost, "/api/d3x/fiche", req, &raw)
	return raw, err
}

// Preview rend une présentation BBCode en HTML, comme sur le site.
func (c *Client) Preview(ctx context.Context, bbcode string) (string, error) {
	var res struct {
		HTML string `json:"html"`
	}
	err := c.doJSON(ctx, nil, http.MethodPost, "/api/d3x/preview", map[string]string{"bbcode": bbcode, "format": "standard"}, &res)
	return res.HTML, err
}

// BBCodeCSS renvoie la feuille de style du rendu BBCode du site.
func (c *Client) BBCodeCSS(ctx context.Context) ([]byte, error) {
	return c.send(ctx, nil, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/api/d3x/bbcode.css", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+c.Key)
		}
		return req, err
	})
}

// Uploaded est la réponse du site à une publication.
type Uploaded struct {
	ID       string `json:"id"`
	Status   string `json:"status"` // active, pending_review
	InfoHash string `json:"infoHash"`
	Bonus    int    `json:"uploadBonus"`
	Download string `json:"download"`
}

// UploadError garde le détail d'un refus (champ fautif, doublon…).
type UploadError struct {
	Status int
	Code   string
	Body   json.RawMessage
}

func (e *UploadError) Error() string { return fmt.Sprintf("V3X a refusé la publication : %s", e.Code) }

// Upload publie une release : le .torrent et les champs de la fiche, comme la
// page d'upload du site.
func (c *Client) Upload(ctx context.Context, torrentName string, raw []byte, fields map[string]string) (*Uploaded, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_ = w.WriteField(k, fields[k])
	}
	fw, _ := w.CreateFormFile("file", torrentName)
	fw.Write(raw)
	w.Close()
	body := buf.Bytes()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/torrents", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("User-Agent", c.ua)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, unreachable(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, &UploadError{Status: resp.StatusCode, Code: e.Error, Body: data}
	}
	var u Uploaded
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// OwnTorrent récupère le .torrent d'une de ses propres releases, même en
// attente de validation (route de l'uploadeur, clé « Upload »).
func (c *Client) OwnTorrent(ctx context.Context, id string) ([]byte, error) {
	return c.send(ctx, &Retry{}, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/torrents/"+url.PathEscape(id)+"/download", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+c.Key)
		}
		return req, err
	})
}
