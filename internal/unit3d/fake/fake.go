// Package fake : un faux tracker UNIT3D pour les tests et le mode démo.
//
// Il reprend ce que fait le vrai (lu dans le code d'UNIT3D) : nom unique,
// description exigée, TVDB refusé sur un film, .torrent réécrit (source,
// private, entropy, sans announce) donc nouvelle empreinte, adresse de
// téléchargement à la clé RSS.
package fake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"baratie/internal/bencode"
)

// Token et RSS : les valeurs acceptées par le faux site.
const (
	Token  = "jeton-de-test"
	RSSKey = "0123456789abcdef0123456789abcdef"
)

// Torrent : une release du faux catalogue.
type Torrent struct {
	ID         int
	Name       string
	Size       int64
	Category   int
	Type       string
	Resolution string
	TMDB       int
	Seeders    int
	Files      []int64
	raw        []byte
}

// Server : le faux site.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	Torrents []*Torrent
	// LastForm : les champs du dernier upload (pour les tests).
	LastForm map[string]string
	LastNFO  []byte
	Uploads  int
	nextID   int
	// Filters : nombre de recherches reçues (tests du Garde-manger).
	Filters int
}

var catNames = map[int]string{1: "Films", 6: "Films VOSTFR"}

// New démarre le faux site, avec un catalogue de départ.
func New(seed ...*Torrent) *Server {
	s := &Server{Torrents: seed, nextID: 500000}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func js(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Limit", "30")
	w.Header().Set("X-RateLimit-Remaining", "25")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/torrent/download/") {
		s.download(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+Token {
		js(w, http.StatusUnauthorized, map[string]string{"message": "Unauthenticated."})
		return
	}
	switch r.URL.Path {
	case "/api/user":
		js(w, 200, map[string]interface{}{"username": "Membre", "group": "Uploader", "uploaded": "12.50 TiB", "downloaded": "1.25 TiB", "ratio": "10.00", "buffer": "11.25 TiB", "seeding": 420, "leeching": 0, "seedbonus": "100000.00", "hit_and_runs": 0})
	case "/api/torrents/filter":
		s.filter(w, r)
	case "/api/torrents/upload":
		s.upload(w, r)
	default:
		js(w, 404, map[string]string{"message": "Not Found"})
	}
}

func (s *Server) filter(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := strings.ToLower(q.Get("name"))
	tmdb, _ := strconv.Atoi(q.Get("tmdbId"))
	cats := map[int]bool{}
	for _, c := range q["categories[]"] {
		n, _ := strconv.Atoi(c)
		cats[n] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Filters++
	data := []interface{}{}
	for _, t := range s.Torrents {
		if name != "" && !strings.Contains(strings.ToLower(t.Name), name) {
			continue
		}
		if tmdb > 0 && t.TMDB != tmdb {
			continue
		}
		if len(cats) > 0 && !cats[t.Category] {
			continue
		}
		files := []map[string]interface{}{}
		for i, sz := range t.Files {
			files = append(files, map[string]interface{}{"id": i + 1, "name": fmt.Sprintf("f%d.mkv", i), "size": sz})
		}
		data = append(data, map[string]interface{}{"type": "torrent", "id": strconv.Itoa(t.ID), "attributes": map[string]interface{}{
			"name": t.Name, "size": t.Size, "seeders": t.Seeders, "leechers": 0, "category": catNames[t.Category], "type": t.Type, "resolution": t.Resolution, "tmdb_id": t.TMDB,
			"info_hash": "AB12", "files": files, "details_link": s.URL + "/torrents/" + strconv.Itoa(t.ID),
			"download_link": fmt.Sprintf("%s/torrent/download/%d.%s", s.URL, t.ID, RSSKey),
		}})
	}
	js(w, 200, map[string]interface{}{"data": data})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		js(w, 400, map[string]interface{}{"success": false, "message": "Validation Error.", "data": "You Must Provide A Torrent File For Upload!"})
		return
	}
	form := map[string]string{}
	for k, v := range r.MultipartForm.Value {
		form[k] = v[0]
	}
	f, _, err := r.FormFile("torrent")
	if err != nil {
		js(w, 400, map[string]interface{}{"success": false, "message": "Validation Error.", "data": "You Must Provide A Torrent File For Upload!"})
		return
	}
	raw, _ := io.ReadAll(f)
	var nfo []byte
	if nf, _, err := r.FormFile("nfo"); err == nil {
		nfo, _ = io.ReadAll(nf)
	}
	d, err := bencode.Decode(raw)
	root, ok := d.Root.(bencode.Dict)
	if err != nil || !ok {
		js(w, 400, map[string]interface{}{"success": false, "message": "Validation Error.", "data": "You Must Provide A Valid Torrent File For Upload!"})
		return
	}
	cat, _ := strconv.Atoi(form["category_id"])
	errs := map[string][]string{}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastForm, s.LastNFO = form, nfo
	for _, t := range s.Torrents {
		if t.Name == form["name"] {
			errs["name"] = append(errs["name"], "The name has already been taken.")
		}
	}
	if strings.TrimSpace(form["description"]) == "" {
		errs["description"] = append(errs["description"], "The description field is required.")
	}
	if form["type_id"] == "" {
		errs["type_id"] = append(errs["type_id"], "The type id field is required.")
	}
	if cat == 0 {
		errs["category_id"] = append(errs["category_id"], "The category id field is required.")
	}
	if form["resolution_id"] == "" {
		errs["resolution_id"] = append(errs["resolution_id"], "The resolution id field is required.")
	}
	// Comme certaines versions d'UNIT3D : tous les identifiants sont exigés
	// (0 = aucun) ; un vrai TVDB sur un film est refusé.
	for _, k := range []string{"tmdb", "imdb", "tvdb", "mal", "igdb"} {
		if _, ok := form[k]; !ok {
			errs[k] = append(errs[k], "Le "+k+" est obligatoire.")
		}
	}
	if form["tvdb"] != "" && form["tvdb"] != "0" && (cat == 1 || cat == 6) {
		errs["tvdb"] = append(errs["tvdb"], "The tvdb must be null.")
	}
	if len(errs) > 0 {
		js(w, 404, map[string]interface{}{"success": false, "message": "Validation Error.", "data": errs})
		return
	}
	// Réécriture comme normalizeTorrent : nouvelle empreinte.
	info := root["info"].(bencode.Dict)
	keep := bencode.Dict{}
	for _, k := range []string{"files", "length", "name", "piece length", "pieces"} {
		if v, ok := info[k]; ok {
			keep[k] = v
		}
	}
	e := make([]byte, 32)
	_, _ = rand.Read(e)
	keep["source"] = []byte("MonTracker")
	keep["private"] = int64(1)
	keep["entropy"] = []byte(hex.EncodeToString(e))
	out := bencode.Dict{"info": keep, "created by": []byte("Faux")}
	site, _ := bencode.Encode(out)
	s.nextID++
	s.Uploads++
	tmdb, _ := strconv.Atoi(form["tmdb"])
	s.Torrents = append(s.Torrents, &Torrent{ID: s.nextID, Name: form["name"], Category: cat, TMDB: tmdb, raw: site})
	url := fmt.Sprintf("%s/torrent/download/%d.%s", s.URL, s.nextID, RSSKey)
	js(w, 200, map[string]interface{}{"success": true, "data": url, "message": "Torrent uploaded successfully."})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/torrent/download/")
	id, key, _ := strings.Cut(rest, ".")
	n, _ := strconv.Atoi(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != RSSKey {
		w.WriteHeader(403)
		return
	}
	for _, t := range s.Torrents {
		if t.ID == n && t.raw != nil {
			// Le vrai ajoute l'announce à ta passkey.
			d, _ := bencode.Decode(t.raw)
			root := d.Root.(bencode.Dict)
			root["announce"] = []byte(s.URL + "/announce/passkey")
			b, _ := bencode.Encode(root)
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.Write(b)
			return
		}
	}
	w.WriteHeader(404)
}
