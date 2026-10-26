package server

// La connexion au tracker où publier, la fiche (TMDB, règles) et la
// publication.

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"baratie/internal/config"
	"baratie/internal/fiche"
	"baratie/internal/tmdb"
	"baratie/internal/tracker"
	"baratie/internal/unit3d"
)

// trackerFor fabrique l'adaptateur du tracker réglé, nil s'il n'y en a pas.
// Variable : les tests et la démo y branchent un faux site.
var trackerFor = func(cfg config.Config, ua string, rules unit3d.Rules) tracker.Tracker {
	if cfg.TrackerURL != "" && cfg.TrackerKey != "" {
		return unit3d.New(cfg.TrackerURL, cfg.TrackerKey, ua, rules)
	}
	return nil
}

// trkState : le tracker et l'état de sa connexion.
type trkState struct {
	mu   sync.Mutex
	t    tracker.Tracker
	acct *tracker.Account
	err  string
}

func (k *trkState) get() (tracker.Tracker, *tracker.Account, string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.t, k.acct, k.err
}

// connectTracker vérifie la clé du tracker réglé.
func (s *Server) connectTracker(ctx context.Context, cfg config.Config) {
	// Le fichier de règles est relu à chaque connexion (« Enregistrer et
	// tester » le prend en compte sans redémarrer).
	rules, found, rerr := unit3d.LoadRules()
	s.mu.Lock()
	s.rulesOK, s.rulesErr = found, ""
	if rerr != nil {
		s.rulesErr = rerr.Error()
	}
	s.mu.Unlock()
	t := trackerFor(cfg, "Baratie/"+s.Version, rules)
	// Tracker connu tout de suite (« configuré »), avant même sa réponse : la
	// page ne doit pas le croire absent pendant la connexion.
	s.trk.mu.Lock()
	if s.trk.t == nil || t == nil {
		s.trk.t, s.trk.acct, s.trk.err = t, nil, ""
	}
	s.trk.mu.Unlock()
	var acct *tracker.Account
	msg := ""
	if t != nil {
		c, cancel := context.WithTimeout(ctx, 20*time.Second)
		a, err := t.Check(c)
		cancel()
		if err != nil {
			msg = err.Error()
		} else {
			acct = a
		}
	}
	s.trk.mu.Lock()
	s.trk.t, s.trk.acct, s.trk.err = t, acct, msg
	s.trk.mu.Unlock()
}

// connectedTracker : le tracker, s'il a accepté la clé.
func (s *Server) connectedTracker() tracker.Tracker {
	t, acct, _ := s.trk.get()
	if t == nil || acct == nil {
		return nil
	}
	return t
}

// trackerOut : l'état du tracker pour la page (jamais la clé).
type trackerOut struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	Username   string `json:"username,omitempty"`
	Group      string `json:"group,omitempty"`
	URL        string `json:"url,omitempty"`
	TMDB       bool   `json:"tmdb"`  // clé TMDB enregistrée
	ImgBB      bool   `json:"imgbb"` // clé imgbb enregistrée
	HasKey     bool   `json:"hasKey"`
	// Rules : fichier de règles (chemin, trouvé, erreur).
	RulesPath  string `json:"rulesPath,omitempty"`
	RulesFound bool   `json:"rulesFound"`
	RulesError string `json:"rulesError,omitempty"`
}

// trackerState : verrou s.mu tenu.
func (s *Server) trackerState() trackerOut {
	t, acct, msg := s.trk.get()
	var o trackerOut
	o.URL, o.HasKey = s.cfg.TrackerURL, s.cfg.TrackerKey != ""
	o.TMDB = s.cfg.TMDBKey != "" || tmdbFallbackKey != ""
	o.ImgBB = s.cfg.ImgBBKey != "" || imgbbFallbackKey != ""
	o.RulesPath, _ = unit3d.RulesPath()
	o.RulesFound, o.RulesError = s.rulesOK, s.rulesErr
	if t == nil {
		return o
	}
	o.ID, o.Name, o.Configured, o.Error = t.ID(), t.Name(), true, msg
	if acct != nil {
		o.OK, o.Username, o.Group = true, acct.Username, acct.Group
	}
	return o
}

// Routes du tracker (la page ne parle jamais au tracker directement).
func (s *Server) routesTracker() {
	s.mux.HandleFunc("GET /ui/trk/context", s.trkContext)
	s.mux.HandleFunc("GET /ui/trk/versions", s.trkVersions)
	s.mux.HandleFunc("POST /ui/pub/{id}/publish-trk", s.trkPublish)
	s.mux.HandleFunc("POST /ui/pub/{id}/simulate-trk", s.trkSimulate)
	s.routesFiche()
	s.routesPantry()
	s.routesShots()
}

func (s *Server) needTracker(w http.ResponseWriter) tracker.Tracker {
	t := s.connectedTracker()
	if t == nil {
		fail(w, http.StatusBadRequest, errors.New("Baratie n'est relié à aucun tracker : vérifie le token dans les réglages"))
	}
	return t
}

func (s *Server) trkContext(w http.ResponseWriter, r *http.Request) {
	if t := s.needTracker(w); t != nil {
		writeJSON(w, http.StatusOK, t.Context())
	}
}

// trkVersions : ce que le tracker a déjà de ce film (tmdb, imdb, nom).
func (s *Server) trkVersions(w http.ResponseWriter, r *http.Request) {
	t := s.needTracker(w)
	if t == nil {
		return
	}
	q := r.URL.Query()
	var in tracker.ExistingQuery
	in.Name = strings.TrimSpace(q.Get("name"))
	in.TMDB, _ = strconv.Atoi(q.Get("tmdb"))
	in.IMDB, _ = strconv.Atoi(q.Get("imdb"))
	for _, c := range q["category"] {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			in.Categories = append(in.Categories, n)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	vs, err := t.Existing(ctx, in)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"versions": vs})
}

// trkPublish : un clic = un envoi. Le NFO arrive en base64 (la page ne poste
// que du JSON).
func (s *Server) trkPublish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fields tracker.Fields `json:"fields"`
		NFO    string         `json:"nfo"`
	}
	if !decode(w, r, 8<<20, &in) {
		return
	}
	var nfo []byte
	if in.NFO != "" {
		b, err := base64.StdEncoding.DecodeString(in.NFO)
		if err != nil {
			fail(w, http.StatusBadRequest, errors.New("NFO illisible"))
			return
		}
		nfo = b
	}
	if err := s.pub.PublishTo(r.PathValue("id"), in.Fields, nfo); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	s.pubGet(w, r)
}

// ---------- TMDB et fiche ----------

// tmdbFallbackKey : clé utilisée quand aucune n'est réglée (mode démo seulement).
var tmdbFallbackKey = ""

func (s *Server) routesFiche() {
	s.mux.HandleFunc("GET /ui/trk/tmdb/search", s.tmdbSearch)
	s.mux.HandleFunc("GET /ui/trk/tmdb/lookup", s.tmdbLookup)
	s.mux.HandleFunc("GET /ui/trk/tmdb/movie/{id}", s.tmdbMovie)
	s.mux.HandleFunc("POST /ui/pub/{id}/fiche-trk", s.trkFiche)
}

func (s *Server) tmdbClient(w http.ResponseWriter) *tmdb.Client {
	s.mu.Lock()
	key := s.cfg.TMDBKey
	s.mu.Unlock()
	if key == "" {
		key = tmdbFallbackKey
	}
	if key == "" {
		fail(w, http.StatusBadRequest, errors.New("clé TMDB manquante : ajoute-la dans les réglages"))
		return nil
	}
	return tmdb.New(key)
}

func (s *Server) tmdbSearch(w http.ResponseWriter, r *http.Request) {
	c := s.tmdbClient(w)
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ms, err := c.Search(ctx, r.URL.Query().Get("q"), r.URL.Query().Get("year"))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": ms})
}

func (s *Server) tmdbLookup(w http.ResponseWriter, r *http.Request) {
	c := s.tmdbClient(w)
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ms, err := c.Lookup(ctx, r.URL.Query().Get("ref"))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": ms})
}

func (s *Server) tmdbMovie(w http.ResponseWriter, r *http.Request) {
	c := s.tmdbClient(w)
	if c == nil {
		return
	}
	id, _ := strconv.Atoi(r.PathValue("id"))
	if id <= 0 {
		fail(w, http.StatusBadRequest, errors.New("film inconnu"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	m, err := c.Details(ctx, id)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// trkFiche bâtit la fiche proposée : jetons du nom, MediaInfo, déductions,
// garde-fous, versions déjà sur le tracker (par TMDB, et par nom).
func (s *Server) trkFiche(w http.ResponseWriter, r *http.Request) {
	t := s.needTracker(w)
	if t == nil {
		return
	}
	var in struct {
		MediaInfo string `json:"mediainfo"`
		TMDB      int    `json:"tmdb"`
		IMDB      int    `json:"imdb"`
		Title     string `json:"title"`
		TMDBTitle string `json:"tmdbTitle"`
		TMDBYear  string `json:"tmdbYear"`
		TMDBLang  string `json:"tmdbLang"`
		TMDBAnim  bool   `json:"tmdbAnim"`
	}
	if !decode(w, r, 4<<20, &in) {
		return
	}
	j, ok := s.pub.Get(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("publication inconnue"))
		return
	}
	ruled, ok := t.(interface{ Rules() fiche.Rules })
	if !ok {
		fail(w, http.StatusInternalServerError, errors.New(t.Name()+" : règles de fiche absentes"))
		return
	}
	rules := ruled.Rules()
	existing := append([]tracker.Version(nil), j.Existing...)
	if in.TMDB > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		vs, err := t.Existing(ctx, tracker.ExistingQuery{TMDB: in.TMDB, Categories: []int{rules.CatFilms, rules.CatFilmsVOSTFR}})
		cancel()
		if err != nil {
			fail(w, http.StatusBadGateway, err)
			return
		}
		seen := map[string]bool{}
		for _, v := range existing {
			seen[v.ID] = true
		}
		for _, v := range vs {
			if !seen[v.ID] {
				existing = append(existing, v)
			}
		}
	}
	files := make([]fiche.File, 0, len(j.Files))
	for _, f := range j.Files {
		files = append(files, fiche.File{Rel: f.Rel, Size: f.Size})
	}
	f := fiche.Build(fiche.Input{Name: j.Name, Title: in.Title, Files: files, MediaInfo: in.MediaInfo, TMDB: in.TMDB, IMDB: in.IMDB, TMDBTitle: in.TMDBTitle, TMDBYear: in.TMDBYear, OrigLang: in.TMDBLang, Animation: in.TMDBAnim, Existing: existing, Rules: rules})
	title := in.Title
	if strings.TrimSpace(title) == "" {
		title = f.Fields.Name
	}
	link, err := s.pub.SeedPreview(j.ID, title)
	out := struct {
		fiche.Fiche
		SeedLink  string `json:"seedLink,omitempty"`
		SeedError string `json:"seedError,omitempty"`
	}{Fiche: f, SeedLink: link}
	if err != nil {
		out.SeedError = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// trkSimulate : exactement ce qui partirait, sans rien envoyer.
func (s *Server) trkSimulate(w http.ResponseWriter, r *http.Request) {
	t := s.needTracker(w)
	if t == nil {
		return
	}
	var in struct {
		Fields tracker.Fields `json:"fields"`
		NFO    string         `json:"nfo"`
	}
	if !decode(w, r, 8<<20, &in) {
		return
	}
	j, ok := s.pub.Get(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, errors.New("publication inconnue"))
		return
	}
	former, ok := t.(interface {
		Form(tracker.Fields) map[string]string
	})
	if !ok {
		fail(w, http.StatusInternalServerError, errors.New(t.Name()+" : simulation indisponible"))
		return
	}
	seedLink, _ := s.pub.SeedPreview(j.ID, in.Fields.Name)
	nfo := 0
	if in.NFO != "" {
		if b, err := base64.StdEncoding.DecodeString(in.NFO); err == nil {
			nfo = len(b)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tracker":  t.Name(),
		"endpoint": "POST /api/torrents/upload",
		"form":     former.Form(in.Fields),
		"torrent":  map[string]interface{}{"name": j.Name + ".torrent", "infoHash": j.InfoHash, "ready": j.State == "ready", "files": len(j.Files), "size": j.Total},
		"nfoBytes": nfo,
		"seed":     map[string]string{"client": j.ClientName, "dir": j.ClientDir, "link": seedLink},
	})
}
