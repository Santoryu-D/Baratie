package unit3d

// Ce qui change d'un tracker UNIT3D à l'autre : son nom, les identifiants de
// sa fiche (catégories, types, résolutions) et ses règles d'upload. Tout vient
// d'un fichier JSON (regles.json dans le dossier de Baratie, ou
// BARATIE_RULES) ; sans fichier, les identifiants par défaut d'UNIT3D et aucune
// règle.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"baratie/internal/config"
	"baratie/internal/fiche"
	"baratie/internal/tracker"
)

// Rules : le fichier de règles.
type Rules struct {
	// Name : le nom du tracker dans la page (défaut : le nom de domaine).
	Name string `json:"name"`
	// Categories : « films » (obligatoire) et « films_vostfr » (facultative :
	// films en VO sous-titrée, rangés à part).
	Categories map[string]int `json:"categories"`
	// Types : fulldisc, remux, encode, web (un seul type pour WEB-DL et
	// WEBRip) ou webdl et webrip, hdtv, dvdr, 3d. Absent : refusé à la fiche.
	Types map[string]int `json:"types"`
	// Resolutions : « 2160p » → identifiant.
	Resolutions map[string]int `json:"resolutions"`
	// BannedTeams : teams refusées (tag → raison). RawOnlyTeams : teams
	// acceptées seulement en brut (Remux, Full Disc).
	BannedTeams  map[string]string `json:"banned_teams"`
	RawOnlyTeams []string          `json:"raw_only_teams"`
	// BannedTokens : mentions du nom refusées (« HDLIGHT » → raison).
	BannedTokens map[string]string `json:"banned_tokens"`
	// Quality : règles d'upload et d'encodage (absent : aucune).
	Quality *Quality `json:"quality"`
}

// Quality : la partie « encodage » du fichier.
type Quality struct {
	// VONeedsFrenchSubs : une VO doit avoir des sous-titres français.
	VONeedsFrenchSubs bool `json:"vo_needs_french_subs"`
	// MultiNeedsOriginal : une release MULTi doit avoir la langue originale.
	MultiNeedsOriginal bool `json:"multi_needs_original"`
	// MinKbps : débit vidéo minimum d'un encode, « bluray/x264/1080p » ou
	// « web/x265/720p » (web : WEBRip) ; MinKbpsAnimation pour l'animation.
	MinKbps          map[string]int `json:"min_kbps"`
	MinKbpsAnimation map[string]int `json:"min_kbps_animation"`
	// Rips SD (DVDRip, BDRip SD) : débit vidéo minimum, AC3 maximum.
	SDMinKbps    int `json:"sd_min_kbps"`
	SDMaxAC3Kbps int `json:"sd_max_ac3_kbps"`
	// Encodes Blu-ray HD : débit audio conseillé.
	HDMinAudioKbps int `json:"hd_min_audio_kbps"`
}

// DefaultRules : les identifiants d'une installation UNIT3D neuve.
func DefaultRules() Rules {
	return Rules{
		Categories:  map[string]int{"films": 1},
		Types:       map[string]int{"fulldisc": 1, "remux": 2, "encode": 3, "webdl": 4, "webrip": 5, "hdtv": 6},
		Resolutions: map[string]int{"4320p": 1, "2160p": 2, "1080p": 3, "1080i": 4, "720p": 5, "576p": 6, "576i": 7, "480p": 8, "480i": 9, "Other": 10},
	}
}

// RulesPath : où lire le fichier de règles.
func RulesPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("BARATIE_RULES")); p != "" {
		return p, nil
	}
	return config.Path("regles.json")
}

// LoadRules lit le fichier de règles (règles par défaut s'il n'existe pas).
// Les clés absentes du fichier gardent leur valeur par défaut.
func LoadRules() (Rules, bool, error) {
	r := DefaultRules()
	p, err := RulesPath()
	if err != nil {
		return r, false, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	var f Rules
	if err := json.Unmarshal(b, &f); err != nil {
		return r, true, fmt.Errorf("%s illisible : %v", p, err)
	}
	if f.Name != "" {
		r.Name = f.Name
	}
	if len(f.Categories) > 0 {
		r.Categories = f.Categories
	}
	if len(f.Types) > 0 {
		r.Types = f.Types
	}
	if len(f.Resolutions) > 0 {
		r.Resolutions = f.Resolutions
	}
	r.BannedTeams, r.RawOnlyTeams, r.BannedTokens, r.Quality = f.BannedTeams, f.RawOnlyTeams, f.BannedTokens, f.Quality
	if r.Categories["films"] == 0 {
		return r, true, fmt.Errorf("%s : la catégorie « films » est obligatoire", p)
	}
	return r, true, nil
}

// withDefaults : le nom tiré de l'adresse s'il manque.
func (r Rules) withDefaults(base string) Rules {
	if strings.TrimSpace(r.Name) == "" {
		r.Name = "le tracker"
		if u, err := url.Parse(strings.TrimSpace(base)); err == nil && u.Hostname() != "" {
			r.Name = strings.TrimPrefix(u.Hostname(), "www.")
		}
	}
	if r.Categories == nil {
		d := DefaultRules()
		r.Categories, r.Types, r.Resolutions = d.Categories, d.Types, d.Resolutions
	}
	return r
}

var typeNames = map[string]string{"fulldisc": "Full Disc", "remux": "Remux", "encode": "Encode", "web": "WEB", "webdl": "WEB-DL", "webrip": "WEBRip", "hdtv": "HDTV", "dvdr": "DVDR", "3d": "3D"}

// context : de quoi remplir les listes de la fiche.
func (r Rules) context() *tracker.PubContext {
	c := &tracker.PubContext{Tracker: "unit3d", Name: r.Name}
	c.Categories = append(c.Categories, tracker.Option{ID: r.Categories["films"], Name: "Films"})
	if id := r.Categories["films_vostfr"]; id > 0 {
		c.Categories = append(c.Categories, tracker.Option{ID: id, Name: "Films VOSTFR"})
	}
	for _, k := range []string{"fulldisc", "remux", "encode", "web", "webdl", "webrip", "hdtv", "dvdr", "3d"} {
		if id := r.Types[k]; id > 0 {
			c.Types = append(c.Types, tracker.Option{ID: id, Name: typeNames[k]})
		}
	}
	for name, id := range r.Resolutions {
		c.Resolutions = append(c.Resolutions, tracker.Option{ID: id, Name: name})
	}
	sort.Slice(c.Resolutions, func(i, j int) bool { return c.Resolutions[i].ID < c.Resolutions[j].ID })
	return c
}

// Fiche : les règles au format de la fiche.
func (r Rules) Fiche() fiche.Rules {
	f := fiche.Rules{
		Tracker:        r.Name,
		CatFilms:       r.Categories["films"],
		CatFilmsVOSTFR: r.Categories["films_vostfr"],
		Types:          map[string]int{},
		Resolutions:    r.Resolutions,
		Banned:         r.BannedTeams,
		RawOnly:        map[string]bool{},
		BannedTokens:   map[string]string{},
	}
	for k, id := range r.Types {
		f.Types[k] = id
	}
	for _, t := range r.RawOnlyTeams {
		f.RawOnly[t] = true
	}
	for k, v := range r.BannedTokens {
		f.BannedTokens[strings.ToUpper(k)] = v
	}
	if q := r.Quality; q != nil {
		f.Quality = &fiche.Quality{
			VONeedsFrenchSubs: q.VONeedsFrenchSubs, MultiNeedsOriginal: q.MultiNeedsOriginal,
			MinKbps: q.MinKbps, MinKbpsAnime: q.MinKbpsAnimation,
			SDMinKbps: q.SDMinKbps, SDMaxAC3: q.SDMaxAC3Kbps, HDMinAudioKbps: q.HDMinAudioKbps,
		}
	}
	return f
}

// Rules : les règles de la fiche (méthode, pour que le serveur les trouve
// sur le tracker).
func (c *Client) Rules() fiche.Rules { return c.r.Fiche() }

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
