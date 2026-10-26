// Package unit3d parle à l'API d'un tracker UNIT3D (/api/user,
// /api/torrents/filter, /api/torrents/upload).
//
// Ce qui change d'un tracker à l'autre (nom, identifiants de la fiche,
// règles d'upload) vient du fichier de règles (rules.go).
//
// Le token ne part qu'en en-tête (jamais dans une adresse) et n'apparaît dans
// aucune erreur. L'adresse de téléchargement rendue par l'upload porte la clé
// RSS du membre : elle n'est ni journalisée ni montrée à la page.
package unit3d

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"baratie/internal/tracker"
)

// Client parle à un UNIT3D avec le token API du membre.
type Client struct {
	Base string
	key  string
	ua   string
	hc   *http.Client
	ctx  *tracker.PubContext
	r    Rules

	mu        sync.Mutex
	pauseTill time.Time // pause demandée par le site (429), partagée
	remaining int       // X-RateLimit-Remaining de la dernière réponse (-1 : inconnu)
}

// Remaining : les requêtes qu'il reste dans la minute, d'après le site.
func (c *Client) Remaining() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remaining, c.remaining >= 0
}

// New crée le client du tracker d'adresse base, avec ses règles.
func New(base, key, ua string, r Rules) *Client {
	r = r.withDefaults(base)
	return &Client{Base: strings.TrimRight(strings.TrimSpace(base), "/"), key: strings.TrimSpace(key), ua: ua, hc: &http.Client{Timeout: 90 * time.Second}, ctx: r.context(), r: r, remaining: -1}
}

var _ tracker.Tracker = (*Client)(nil)

func (c *Client) ID() string   { return "unit3d" }
func (c *Client) Name() string { return c.r.Name }

// TorrentURL : la fiche sur le site.
func (c *Client) TorrentURL(id string) string { return c.Base + "/torrents/" + url.PathEscape(id) }

// Context : catégories, types et résolutions du tracker.
func (c *Client) Context() *tracker.PubContext { return c.ctx }

// APIError : une erreur rendue par le site.
type APIError struct {
	Tracker string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return e.Tracker + " refuse le token API : vérifie-le (sur le site : ton profil ▸ Paramètres ▸ API)"
	case http.StatusForbidden:
		return e.Tracker + " refuse l'accès (compte banni, désactivé, ou droits d'upload retirés ?)"
	case http.StatusTooManyRequests:
		return e.Tracker + " limite les appels, réessaie dans un moment"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s : %s (HTTP %d)", e.Tracker, e.Message, e.Status)
	}
	return fmt.Sprintf("%s : HTTP %d", e.Tracker, e.Status)
}

// unreachable : erreur réseau sans l'adresse (elle peut porter une clé).
func (c *Client) unreachable(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s injoignable : %v", c.r.Name, ue.Err)
	}
	return fmt.Errorf("%s injoignable : %w", c.r.Name, err)
}

const maxPause = 2 * time.Minute

func retryAfter(h http.Header, attempt int) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if s, err := strconv.Atoi(v); err == nil {
			return time.Duration(s)*time.Second + time.Second
		}
	}
	return time.Duration(2<<attempt) * time.Second
}

// do envoie la requête ; un 429 attend la pause demandée (3 essais au plus).
func (c *Client) do(ctx context.Context, build func() (*http.Request, error), auth bool, limit int64) ([]byte, int, error) {
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		d := time.Until(c.pauseTill)
		c.mu.Unlock()
		if d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil, 0, ctx.Err()
			case <-t.C:
			}
		}
		req, err := build()
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", c.ua)
		req.Header.Set("Accept", "application/json")
		if auth {
			req.Header.Set("Authorization", "Bearer "+c.key)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, 0, c.unreachable(err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
		resp.Body.Close()
		if v := resp.Header.Get("X-RateLimit-Remaining"); v != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				c.mu.Lock()
				c.remaining = n
				c.mu.Unlock()
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			d := retryAfter(resp.Header, attempt)
			if d > maxPause {
				return data, resp.StatusCode, &APIError{Tracker: c.r.Name, Status: resp.StatusCode}
			}
			c.mu.Lock()
			if till := time.Now().Add(d); till.After(c.pauseTill) {
				c.pauseTill = till
			}
			c.mu.Unlock()
			continue
		}
		return data, resp.StatusCode, nil
	}
}

// apiErr : message lisible d'une réponse en erreur.
func (c *Client) apiErr(status int, data []byte) error {
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &e)
	return &APIError{Tracker: c.r.Name, Status: status, Message: e.Message}
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out interface{}) error {
	u := c.Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	data, status, err := c.do(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	}, true, 16<<20)
	if err != nil {
		return err
	}
	if status >= 300 {
		return c.apiErr(status, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New(c.r.Name + " a répondu autre chose que du JSON (page de maintenance ?)")
	}
	return nil
}

// Check lit le compte lié au token (GET /api/user).
func (c *Client) Check(ctx context.Context) (*tracker.Account, error) {
	if c.key == "" {
		return nil, errors.New("token API du tracker manquant")
	}
	var u struct {
		Username string `json:"username"`
		Group    string `json:"group"`
		Ratio    string `json:"ratio"`
	}
	if err := c.getJSON(ctx, "/api/user", nil, &u); err != nil {
		return nil, err
	}
	if u.Username == "" {
		return nil, errors.New(c.r.Name + " : réponse de /api/user inattendue")
	}
	a := &tracker.Account{Username: u.Username, Group: u.Group}
	if r, err := strconv.ParseFloat(strings.TrimSpace(u.Ratio), 64); err == nil {
		a.Ratio = &r
	}
	return a, nil
}

// flexInt lit un nombre rendu en nombre ou en texte.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		if x, err2 := strconv.ParseFloat(s, 64); err2 == nil {
			*f = flexInt(x)
			return nil
		}
		return nil // valeur inattendue : ignorée plutôt que de tout refuser
	}
	*f = flexInt(n)
	return nil
}

type filterResp struct {
	Data []struct {
		ID         flexInt `json:"id"`
		Attributes struct {
			Name       string  `json:"name"`
			Size       flexInt `json:"size"`
			Seeders    flexInt `json:"seeders"`
			Leechers   flexInt `json:"leechers"`
			Category   string  `json:"category"`
			Type       string  `json:"type"`
			Resolution string  `json:"resolution"`
			InfoHash   string  `json:"info_hash"`
			TMDB       flexInt `json:"tmdb_id"`
			Details    string  `json:"details_link"`
			Files      []struct {
				Size flexInt `json:"size"`
			} `json:"files"`
		} `json:"attributes"`
	} `json:"data"`
}

// Existing cherche ce que le tracker a déjà (GET /api/torrents/filter).
func (c *Client) Existing(ctx context.Context, q tracker.ExistingQuery) ([]tracker.Version, error) {
	v := url.Values{"perPage": {"50"}}
	if q.Name != "" {
		v.Set("name", q.Name)
	}
	if q.TMDB > 0 {
		v.Set("tmdbId", strconv.Itoa(q.TMDB))
	}
	if q.IMDB > 0 {
		v.Set("imdbId", strconv.Itoa(q.IMDB))
	}
	for _, id := range q.Categories {
		v.Add("categories[]", strconv.Itoa(id))
	}
	if len(v) == 1 {
		return nil, errors.New("recherche vide")
	}
	var r filterResp
	if err := c.getJSON(ctx, "/api/torrents/filter", v, &r); err != nil {
		return nil, err
	}
	out := make([]tracker.Version, 0, len(r.Data))
	for _, d := range r.Data {
		id := strconv.FormatInt(int64(d.ID), 10)
		a := d.Attributes
		v := tracker.Version{ID: id, Name: a.Name, Size: int64(a.Size), Category: a.Category, Type: a.Type, Resolution: a.Resolution, Seeders: int(a.Seeders), Leechers: int(a.Leechers), URL: c.TorrentURL(id), InfoHash: strings.ToLower(a.InfoHash), TMDB: int(a.TMDB)}
		if strings.HasPrefix(a.Details, c.Base+"/") {
			v.URL = a.Details
		}
		for _, f := range a.Files {
			v.Files = append(v.Files, int64(f.Size))
		}
		out = append(out, v)
	}
	return out, nil
}

var downloadID = regexp.MustCompile(`/download/(\d+)`)

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Form : les champs exacts envoyés à /api/torrents/upload (hors fichiers).
// Sert aussi à la simulation : ce que montre « Simuler » est ce qui part.
func Form(f tracker.Fields) map[string]string {
	fields := map[string]string{
		"name":             f.Name,
		"description":      f.Description,
		"mediainfo":        f.MediaInfo,
		"bdinfo":           f.BDInfo,
		"category_id":      strconv.Itoa(f.CategoryID),
		"type_id":          strconv.Itoa(f.TypeID),
		"anonymous":        b01(f.Anonymous),
		"stream":           b01(f.Stream),
		"sd":               b01(f.SD),
		"personal_release": b01(f.Personal),
		"internal":         "0",
		"featured":         "0",
		"free":             "0",
		"doubleup":         "0",
		"sticky":           "0",
		"refundable":       "0",
		"keywords":         f.Keywords,
	}
	if f.ResolutionID > 0 {
		fields["resolution_id"] = strconv.Itoa(f.ResolutionID)
	}
	// Tous les identifiants partent, à 0 quand il n'y en a pas : certaines
	// versions d'UNIT3D les exigent tous (« Le tvdb est obligatoire », « Le igdb
	// est obligatoire »), les récentes lisent 0 comme « aucun ».
	for k, v := range map[string]int{"tmdb": f.TMDB, "imdb": f.IMDB, "tvdb": f.TVDB, "mal": f.MAL, "igdb": 0} {
		if v < 0 {
			v = 0
		}
		fields[k] = strconv.Itoa(v)
	}
	return fields
}

// Form (méthode) : pour la simulation côté serveur.
func (c *Client) Form(f tracker.Fields) map[string]string { return Form(f) }

// Upload publie la release (POST /api/torrents/upload).
func (c *Client) Upload(ctx context.Context, torrentName string, raw, nfo []byte, f tracker.Fields) (*tracker.Uploaded, error) {
	if strings.TrimSpace(f.Name) == "" || strings.TrimSpace(f.Description) == "" || f.CategoryID == 0 || f.TypeID == 0 {
		return nil, errors.New("fiche incomplète : nom, description, catégorie et type sont exigés")
	}
	fields := Form(f)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, k := range sortedKeys(fields) {
		_ = w.WriteField(k, fields[k])
	}
	fw, _ := w.CreateFormFile("torrent", torrentName)
	fw.Write(raw)
	if len(nfo) > 0 {
		nw, _ := w.CreateFormFile("nfo", strings.TrimSuffix(torrentName, ".torrent")+".nfo")
		nw.Write(nfo)
	}
	w.Close()
	body, ctype := buf.Bytes(), w.FormDataContentType()
	data, status, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/torrents/upload", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", ctype)
		}
		return req, err
	}, true, 4<<20)
	if err != nil {
		return nil, err
	}
	var r struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	_ = json.Unmarshal(data, &r)
	if status >= 300 || !r.Success {
		msg := r.Message
		if detail := validationText(r.Data); detail != "" {
			msg = strings.TrimSuffix(msg, ".") + " : " + detail
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", status)
		}
		return nil, &tracker.UploadError{Status: status, Code: msg, Body: clip(string(data))}
	}
	var dl string
	if err := json.Unmarshal(r.Data, &dl); err != nil || !strings.HasPrefix(dl, "http") {
		return nil, errors.New(c.r.Name + " a accepté la release mais n'a pas rendu l'adresse du .torrent : récupère-le sur la fiche")
	}
	up := &tracker.Uploaded{Download: dl}
	if m := downloadID.FindStringSubmatch(dl); m != nil {
		up.ID = m[1]
		up.URL = c.TorrentURL(m[1])
	}
	return up, nil
}

// validationText : « name : The name has already been taken. ; type_id : … ».
func validationText(raw json.RawMessage) string {
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	parts := []string{}
	for _, k := range sortedKeys(m) {
		switch v := m[k].(type) {
		case []interface{}:
			for _, x := range v {
				parts = append(parts, fmt.Sprintf("%s : %v", k, x))
			}
		default:
			parts = append(parts, fmt.Sprintf("%s : %v", k, v))
		}
	}
	return strings.Join(parts, " ; ")
}

// SeedTorrent télécharge le .torrent réécrit par le site (adresse à la clé RSS
// du membre, rendue par l'upload).
func (c *Client) SeedTorrent(ctx context.Context, up *tracker.Uploaded) ([]byte, error) {
	if up == nil || up.Download == "" {
		return nil, errors.New("adresse du .torrent inconnue : récupère-le sur la fiche")
	}
	data, status, err := c.do(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, up.Download, nil)
	}, false, 32<<20)
	if err != nil {
		return nil, err
	}
	if status >= 300 || len(data) == 0 || data[0] != 'd' {
		return nil, fmt.Errorf("%s n'a pas rendu le .torrent (HTTP %d) : récupère-le sur la fiche", c.r.Name, status)
	}
	return data, nil
}

func clip(s string) string {
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}
