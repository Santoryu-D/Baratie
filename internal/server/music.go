package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/V3x0rV3x/D3X/internal/audio"
	"github.com/V3x0rV3x/D3X/internal/musicbrainz"
)

// Musique : l'album d'une release cherché dans MusicBrainz et comparé à ses
// pistes (nombre, durées). D3X interroge MusicBrainz lui-même, au rythme
// d'une requête par seconde.

func (s *Server) routesMusic() {
	s.mux.HandleFunc("POST /ui/pub/{id}/mb/search", s.mbSearch)
	s.mux.HandleFunc("POST /ui/pub/{id}/mb/decide", s.mbDecide)
	s.mux.HandleFunc("GET /ui/pub/{id}/mb/release/{mbid}", s.mbRelease)
}

// Nom de dossier habituel : « Artiste - Album (2015) [FLAC] ».
var folderRe = regexp.MustCompile(`^\s*(.+?)\s+[-–]\s+(.+?)\s*(?:[(\[]\s*((?:19|20)\d\d)\s*[)\]])?\s*(?:[(\[].*)?$`)

// localOf : ce que la release dit d'elle-même, tags d'abord, nom ensuite.
func localOf(name string, a *audio.Album) musicbrainz.Local {
	l := musicbrainz.Local{}
	if a != nil {
		l.Artist, l.Album, l.Year, l.Media, l.MBRelease = a.Artist, a.Title, a.Year, a.Media, a.MBRelease
		for _, t := range a.Tracks {
			l.Durations = append(l.Durations, t.Duration)
		}
	}
	if l.Album == "" || l.Artist == "" {
		clean := strings.NewReplacer("_", " ").Replace(name)
		if m := folderRe.FindStringSubmatch(clean); m != nil {
			if l.Artist == "" {
				l.Artist = m[1]
			}
			if l.Album == "" {
				l.Album = m[2]
			}
			if l.Year == "" {
				l.Year = m[3]
			}
		}
	}
	return l
}

func (s *Server) mbLocal(w http.ResponseWriter, id string) (musicbrainz.Local, bool) {
	j, ok := s.pub.Get(id)
	if !ok {
		fail(w, http.StatusNotFound, errors.New("publication inconnue"))
		return musicbrainz.Local{}, false
	}
	return localOf(j.Name, j.Audio), true
}

func (s *Server) mbSearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Q string `json:"q"`
	}
	if !decode(w, r, 1<<12, &in) {
		return
	}
	l, ok := s.mbLocal(w, r.PathValue("id"))
	if !ok {
		return
	}
	artist, album := l.Artist, l.Album
	if q := strings.TrimSpace(in.Q); q != "" {
		artist, album = "", q
		if a, b, cut := strings.Cut(q, " - "); cut {
			artist, album = strings.TrimSpace(a), strings.TrimSpace(b)
		}
	}
	if album == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"candidates": []interface{}{}, "artist": artist, "album": album})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	found, err := s.mb.Search(ctx, artist, album, 25)
	if err == nil && len(found) == 0 && artist != "" {
		found, err = s.mb.Search(ctx, "", artist+" "+album, 25)
	}
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	// Les trois premières éditions au bon nombre de pistes sont lues pour
	// comparer les durées.
	list, err := s.mb.Rank(ctx, l, found, 3)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if len(list) > 15 {
		list = list[:15]
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"candidates": list, "artist": artist, "album": album})
}

func (s *Server) mbDecide(w http.ResponseWriter, r *http.Request) {
	l, ok := s.mbLocal(w, r.PathValue("id"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	res, err := s.mb.Decide(ctx, l)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) mbRelease(w http.ResponseWriter, r *http.Request) {
	l, ok := s.mbLocal(w, r.PathValue("id"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rel, err := s.mb.Lookup(ctx, r.PathValue("mbid"))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	c := musicbrainz.Compare(l, *rel)
	c.FromTags = l.MBRelease != "" && l.MBRelease == rel.ID
	writeJSON(w, http.StatusOK, c)
}
