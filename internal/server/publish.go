package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// Routes de la publication. La page ne parle jamais au site directement : D3X
// relaie, avec la clé du membre, qu'elle ne voit pas.

func (s *Server) routesPublish() {
	s.mux.HandleFunc("POST /ui/pub/start", s.pubStart)
	s.mux.HandleFunc("GET /ui/pub/context", s.pubContext)
	s.mux.HandleFunc("GET /ui/pub/tmdb/{kind}", s.pubTMDB)
	s.mux.HandleFunc("POST /ui/pub/preview", s.pubPreview)
	s.mux.HandleFunc("GET /ui/bbcode.css", s.pubCSS)
	s.mux.HandleFunc("GET /ui/pub/{id}", s.pubGet)
	s.mux.HandleFunc("POST /ui/pub/{id}/cancel", s.pubCancel)
	s.mux.HandleFunc("GET /ui/pub/{id}/file/{index}", s.pubFile)
	s.mux.HandleFunc("POST /ui/pub/{id}/fiche", s.pubFiche)
	s.mux.HandleFunc("POST /ui/pub/{id}/clientdir", s.pubClientDir)
	s.mux.HandleFunc("POST /ui/pub/{id}/client", s.pubClient)
	s.mux.HandleFunc("POST /ui/pub/{id}/publish", s.pubPublish)
}

func decode(w http.ResponseWriter, r *http.Request, max int64, v interface{}) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, max)).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, errors.New("requête illisible"))
		return false
	}
	return true
}

// v3xClient : V3X seul (la fiche, TMDB, les demandes n'ont pas besoin du
// client torrent).
func (s *Server) v3xClient() (*v3x.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.v == nil || s.me == nil {
		return nil, errors.New("D3X n'est pas connecté à V3X : vérifie ta clé API dans les réglages")
	}
	return s.v, nil
}

// apiFail transmet une erreur du site avec son code HTTP.
func apiFail(w http.ResponseWriter, err error) {
	var ae *v3x.APIError
	if errors.As(err, &ae) {
		status := ae.Status
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			// La clé n'a peut-être pas le droit « Upload ».
			writeJSON(w, status, map[string]string{"error": ae.Code, "message": "V3X refuse : ta clé API doit avoir le droit « Upload » (Réglages ▸ Intégrations)"})
			return
		}
		writeJSON(w, status, map[string]string{"error": ae.Code})
		return
	}
	fail(w, http.StatusBadGateway, err)
}

func (s *Server) pubStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	if !decode(w, r, 1<<14, &in) {
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		fail(w, http.StatusBadRequest, errors.New("choisis un fichier ou un dossier"))
		return
	}
	src, err := s.source(in.Source)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	j, err := s.pub.Start(src, in.Path)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	// Le dossier parent revient au prochain lancement du sélecteur.
	if src.ID() == "local" {
		_ = s.updateConfig(func(c *config.Config) { c.LastDir = filepath.Dir(j.Path) })
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) pubGet(w http.ResponseWriter, r *http.Request) {
	j, ok := s.pub.Get(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("publication inconnue"))
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) pubCancel(w http.ResponseWriter, r *http.Request) {
	s.pub.Cancel(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// pubFile sert un morceau d'un fichier de la release (MediaInfo lit le fichier
// par morceaux, dans la page), lu sur ce PC ou sur la seedbox. Rien d'autre
// n'est lisible.
func (s *Server) pubFile(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		fail(w, http.StatusBadRequest, errors.New("fichier inconnu"))
		return
	}
	q := r.URL.Query()
	off, _ := strconv.ParseInt(q.Get("offset"), 10, 64)
	n, _ := strconv.ParseInt(q.Get("size"), 10, 64)
	b, size, err := s.pub.ReadFile(r.Context(), r.PathValue("id"), idx, off, n)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-File-Size", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// Contexte du membre gardé 5 minutes (catégories, équipe, modèles).
func (s *Server) pubContext(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cached, at := s.pubCtx, s.pubCtxAt
	s.mu.Unlock()
	if cached != nil && time.Since(at) < 5*time.Minute && r.URL.Query().Get("fresh") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(cached)
		return
	}
	v, err := s.v3xClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	raw, err := v.PublishContext(ctx)
	if err != nil {
		apiFail(w, err)
		return
	}
	s.mu.Lock()
	s.pubCtx, s.pubCtxAt = raw, time.Now()
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(raw)
}

func (s *Server) pubTMDB(w http.ResponseWriter, r *http.Request) {
	v, err := s.v3xClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	raw, err := v.TMDB(ctx, r.PathValue("kind"), r.URL.Query())
	if err != nil {
		apiFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(raw)
}

func (s *Server) pubFiche(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ReleaseName  string  `json:"releaseName"`
		MediaInfo    string  `json:"mediainfo"`
		TmdbRef      *string `json:"tmdbRef"`
		MbRef        *string `json:"mbRef"`
		CategorySlug *string `json:"categorySlug"`
		TemplateID   *string `json:"templateId"`
	}
	if !decode(w, r, 4<<20, &in) {
		return
	}
	dl, ok := s.pub.Download(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("publication inconnue"))
		return
	}
	v, err := s.v3xClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := v.Fiche(ctx, v3x.FicheRequest{ReleaseName: in.ReleaseName, MediaInfo: in.MediaInfo, TmdbRef: in.TmdbRef, MbRef: in.MbRef, Download: dl, CategorySlug: in.CategorySlug, TemplateID: in.TemplateID})
	if err != nil {
		apiFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(raw)
}

func (s *Server) pubPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BBCode string `json:"bbcode"`
	}
	if !decode(w, r, 1<<20, &in) {
		return
	}
	v, err := s.v3xClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	html, err := v.Preview(ctx, in.BBCode)
	if err != nil {
		apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": html})
}

// pubCSS relaie la feuille de style du rendu BBCode du site (gardée 1 h).
func (s *Server) pubCSS(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	css, at := s.bbCSS, s.bbCSSAt
	s.mu.Unlock()
	if css == nil || time.Since(at) > time.Hour {
		if v, err := s.v3xClient(); err == nil {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			if b, err := v.BBCodeCSS(ctx); err == nil {
				css = b
				s.mu.Lock()
				s.bbCSS, s.bbCSSAt = b, time.Now()
				s.mu.Unlock()
			}
			cancel()
		}
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(css)
}

func (s *Server) pubClientDir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir      string `json:"dir"`
		Remember bool   `json:"remember"`
	}
	if !decode(w, r, 1<<14, &in) {
		return
	}
	rule, cid, err := s.pub.SetClientDir(r.PathValue("id"), in.Dir)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	// Retenue dans les dossiers du client qui seedera la release.
	if in.Remember && rule != nil {
		err := s.updateConfig(func(c *config.Config) {
			cc := c.Client(cid)
			if cc == nil {
				return
			}
			rules := []pathmap.Rule{*rule}
			for _, x := range cc.PathMap {
				if x.From != rule.From {
					rules = append(rules, x)
				}
			}
			cc.PathMap = rules
		})
		if err != nil {
			fail(w, http.StatusInternalServerError, errors.New("dossier non retenu : "+err.Error()))
			return
		}
	}
	s.pubGet(w, r)
}

// pubClient : le client qui seedera la release, choisi par le membre.
func (s *Server) pubClient(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Client string `json:"client"`
	}
	if !decode(w, r, 1<<12, &in) {
		return
	}
	if err := s.pub.SetClient(r.PathValue("id"), in.Client); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.pubGet(w, r)
}

func (s *Server) pubPublish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fields map[string]string `json:"fields"`
	}
	if !decode(w, r, 4<<20, &in) {
		return
	}
	// Champs acceptés : ceux de la page d'upload du site, rien d'autre.
	allowed := map[string]bool{"categoryId": true, "description": true, "descriptionFormat": true, "title": true, "releaseName": true, "nfo": true, "language": true, "season": true, "tmdbUrl": true, "noTmdb": true, "anonymous": true, "exclusive": true, "teamRelease": true, "posterUrl": true, "creator": true}
	fields := map[string]string{"rightsDeclared": "true", "sessionId": "d3x-" + time.Now().UTC().Format("20060102")}
	for k, v := range in.Fields {
		if allowed[k] {
			fields[k] = v
		}
	}
	if fields["categoryId"] == "" {
		fail(w, http.StatusBadRequest, errors.New("choisis une catégorie"))
		return
	}
	if err := s.pub.Publish(r.PathValue("id"), fields); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	s.pubGet(w, r)
}
