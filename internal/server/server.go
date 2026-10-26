// Package server sert la page de Baratie.
//
// Protections, parce qu'une page web quelconque ouverte dans le même
// navigateur peut tenter d'appeler ce serveur :
//   - en mode local, écoute sur 127.0.0.1 et en-tête Host vérifié (bloque le
//     rebond DNS) ;
//   - toute route /ui/ exige le cookie de session, obtenu par le lien
//     d'ouverture qui porte le jeton (HttpOnly, SameSite=Strict) ;
//   - les requêtes POST exigent du JSON et une origine identique.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"baratie/internal/client"
	"baratie/internal/config"
	"baratie/internal/journal"
	"baratie/internal/pantry"
	"baratie/internal/pathmap"
	"baratie/internal/publish"
	"baratie/internal/releases"
	"baratie/internal/seed"
)

//go:embed ui
var uiFS embed.FS

const cookieName = "baratie_session"

// Server est la page et son état.
type Server struct {
	Version string
	Token   string
	Port    int
	opts    Options

	mu       sync.Mutex
	cfg      *config.Config
	conns    map[string]*clientConn // clients torrent, par identifiant
	trk      trkState               // le tracker où publier
	rulesErr string                 // fichier de règles illisible
	rulesOK  bool                   // fichier de règles trouvé
	pantry   *pantry.Manager
	watcher  *seed.Watcher
	pub      *publish.Manager
	mux      *http.ServeMux

	lastSeen atomic.Int64
}

// NewToken tire un jeton de session.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Options : mode serveur (Docker, NAS), où la page est ouverte depuis une
// autre machine.
type Options struct {
	// Remote : page jointe par le réseau (n'importe quel nom d'hôte, derrière
	// un proxy https compris). Le jeton reste exigé, le cookie dure.
	Remote bool
}

// New prépare le serveur avec la configuration lue.
func New(version string, cfg *config.Config, token string, port int, opts Options) *Server {
	s := &Server{Version: version, Token: token, Port: port, opts: opts, cfg: cfg, mux: http.NewServeMux()}
	s.watcher = seed.New(s.env)
	s.pub = publish.New(s.env, s.watcher, version)
	sub, _ := fs.Sub(uiFS, "ui")
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))
	s.mux.HandleFunc("GET /auth", s.auth)
	s.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("Baratie")) })
	s.mux.HandleFunc("GET /ui/state", s.state)
	s.mux.HandleFunc("POST /ui/settings", s.settings)
	s.mux.HandleFunc("POST /ui/reconnect", s.reconnect)
	s.mux.HandleFunc("GET /ui/history", s.history)
	s.mux.HandleFunc("GET /ui/fs", s.browse)
	s.mux.HandleFunc("GET /ui/client/detect", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"found": client.Detect()})
	})
	s.mux.HandleFunc("POST /ui/client/test", s.clientTest)
	s.routesPublish()
	s.routesTracker()
	go s.connect()
	return s
}

func (s *Server) hostAllowed(host string) bool {
	if s.opts.Remote {
		return host != ""
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil || p != strconv.Itoa(s.Port) {
		return false
	}
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.hostAllowed(r.Host) {
		http.Error(w, "hôte refusé", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host && o != "https://"+r.Host {
			http.Error(w, "origine refusée", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "JSON attendu", http.StatusUnsupportedMediaType)
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/ui/") {
		if !s.sessionOK(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "session", "remote": s.opts.Remote})
			return
		}
		if s.lastSeen.Swap(time.Now().UnixNano()) == 0 {
			log.Println("page de Baratie ouverte")
		}
	}
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// Aucun script venu d'ailleurs. WebAssembly pour MediaInfo ; images en
	// https (affiches TMDB, captures hébergées).
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self' 'wasm-unsafe-eval'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	h.Set("Cache-Control", "no-store")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) sessionOK(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.Token)) == 1
}

// auth échange le jeton du lien d'ouverture contre le cookie de session.
func (s *Server) auth(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.Token)) != 1 {
		http.Error(w, "Lien invalide : le bon lien est dans le journal de Baratie (docker logs baratie).", http.StatusForbidden)
		return
	}
	c := &http.Cookie{Name: cookieName, Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}
	if s.opts.Remote {
		// Page ouverte d'une autre machine : le cookie tient 400 jours
		// (https derrière un proxy : Secure).
		c.MaxAge = 400 * 24 * 3600
		c.Secure = r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	http.SetCookie(w, c)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// decode lit le JSON de la page (taille bornée).
func decode(w http.ResponseWriter, r *http.Request, max int64, v interface{}) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, max)).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, errors.New("requête illisible"))
		return false
	}
	return true
}

// connect (re)crée les clients et vérifie les connexions : le tracker et
// chaque client torrent, tous en même temps.
func (s *Server) connect() {
	s.mu.Lock()
	cfg := *s.cfg
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.connectTracker(ctx, cfg) }()
	conns := dialAll(ctx, cfg.Clients)
	<-done
	s.mu.Lock()
	s.conns = conns
	s.mu.Unlock()
}

// updateConfig change et enregistre la configuration.
func (s *Server) updateConfig(f func(*config.Config)) error {
	s.mu.Lock()
	c := *s.cfg
	c.Clients = append([]config.ClientConfig(nil), c.Clients...)
	f(&c)
	s.cfg = &c
	s.mu.Unlock()
	return config.Save(&c)
}

type stateOut struct {
	Version string     `json:"version"`
	Mode    string     `json:"mode,omitempty"` // server : page jointe par le réseau
	Tracker trackerOut `json:"tracker"`
	Pantry  pantryOut  `json:"pantry"`
	// Clients : les clients torrent, dans l'ordre des réglages, chacun avec
	// son état et ses dossiers.
	Clients  []clientOut `json:"clients"`
	Category string      `json:"category"`
	Tag      string      `json:"tag"`
	LastDir  string      `json:"lastDir,omitempty"`
	SeedDir  string      `json:"seedDir,omitempty"`
	Theme    string      `json:"theme"`
}

// state ne renvoie JAMAIS de clé ni de mot de passe : seulement leur présence.
func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var o stateOut
	o.Version = s.Version
	o.Tracker = s.trackerState()
	o.Pantry = pantrySettings(*s.cfg)
	o.Clients = s.clientsOut()
	o.Category, o.Tag, o.LastDir = s.cfg.Category, s.cfg.Tag, s.cfg.LastDir
	o.SeedDir = publish.SeedDir(s.cfg)
	o.Theme = "light"
	if s.cfg.Theme == "dark" {
		o.Theme = "dark"
	}
	if s.opts.Remote {
		o.Mode = "server"
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TrackerURL *string     `json:"trackerUrl"` // absent = inchangée
		TrackerKey string      `json:"trackerKey"` // vide = inchangé
		TMDBKey    string      `json:"tmdbKey"`    // vide = inchangé
		ImgBBKey   string      `json:"imgbbKey"`   // vide = inchangé
		Pantry     *pantryIn   `json:"pantry"`     // absent = inchangé
		Category   *string     `json:"category"`
		Tag        *string     `json:"tag"`
		Theme      *string     `json:"theme"`
		Clients    *[]clientIn `json:"clients"` // la liste complète ; absente = inchangée
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, errors.New("réglages illisibles"))
		return
	}
	s.mu.Lock()
	c := *s.cfg
	// Champ secret laissé vide = on garde l'ancien (la page ne les connaît pas).
	if in.TrackerURL != nil {
		u := strings.TrimRight(strings.TrimSpace(*in.TrackerURL), "/")
		if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			u = "https://" + u
		}
		if u != "" {
			if p, err := url.Parse(u); err != nil || p.Host == "" {
				s.mu.Unlock()
				fail(w, http.StatusBadRequest, errors.New("adresse du tracker illisible"))
				return
			}
		}
		c.TrackerURL = u
	}
	if k := strings.TrimSpace(in.TrackerKey); k != "" {
		c.TrackerKey = k
	}
	if k := strings.TrimSpace(in.TMDBKey); k != "" {
		c.TMDBKey = k
	}
	if k := strings.TrimSpace(in.ImgBBKey); k != "" {
		c.ImgBBKey = k
	}
	if in.Pantry != nil {
		in.Pantry.apply(&c)
	}
	if in.Clients != nil {
		list, err := mergeClients(c.Clients, *in.Clients)
		if err != nil {
			s.mu.Unlock()
			fail(w, http.StatusBadRequest, err)
			return
		}
		c.Clients = list
	}
	if in.Category != nil {
		c.Category = strings.TrimSpace(*in.Category)
	}
	if in.Tag != nil {
		c.Tag = strings.TrimSpace(*in.Tag)
	}
	if in.Theme != nil {
		c.Theme = ""
		if *in.Theme == "dark" {
			c.Theme = "dark"
		}
	}
	s.cfg = &c
	s.mu.Unlock()
	if err := config.Save(&c); err != nil {
		fail(w, http.StatusInternalServerError, errors.New("réglages non enregistrés : "+withPermHint(err).Error()))
		return
	}
	s.connect()
	s.state(w, r)
}

// Busy : une publication ou une vérification est en route.
func (s *Server) Busy() bool {
	return s.pub.Busy() || s.watcher.Busy()
}

func (s *Server) reconnect(w http.ResponseWriter, r *http.Request) {
	s.connect()
	s.state(w, r)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": journal.Last(300)})
}

// browse liste un dossier pour choisir une release. Sans chemin : les
// points de départ.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	exclude := s.cfg.ExcludeList()
	s.mu.Unlock()
	l, err := releases.Browse(r.URL.Query().Get("path"), exclude)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// cleanRules : règles de dossiers complètes (les deux côtés), sans doublon.
func cleanRules(in []pathmap.Rule) ([]pathmap.Rule, error) {
	out := []pathmap.Rule{}
	seen := map[string]bool{}
	for _, r := range in {
		r.From, r.To = strings.TrimSpace(r.From), strings.TrimSpace(r.To)
		if r.From == "" && r.To == "" {
			continue
		}
		if r.From == "" || r.To == "" {
			return nil, errors.New("dossiers du client : remplis les deux côtés de chaque ligne (ou vide-la)")
		}
		if len(out) >= 50 {
			return nil, errors.New("dossiers du client : 50 lignes au plus")
		}
		k := strings.ToLower(r.From + "|" + r.To)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out, nil
}

// maskURL cache le mot de passe écrit dans une adresse (« user:pass@hôte »).
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); !has {
		return raw
	}
	u.User = url.UserPassword(u.User.Username(), "***")
	return strings.Replace(u.String(), "%2A%2A%2A", "***", 1)
}

// lastDir retient le dossier parent du dernier choix (prochaine ouverture du
// sélecteur).
func (s *Server) lastDir(path string) {
	_ = s.updateConfig(func(c *config.Config) { c.LastDir = filepath.Dir(path) })
}
