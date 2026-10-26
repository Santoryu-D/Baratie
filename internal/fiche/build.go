package fiche

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"baratie/internal/tracker"
)

// Genres de release (traduits en identifiants par les Rules du tracker).
const (
	TypeFullDisc = "fulldisc"
	TypeDVDR     = "dvdr"
	TypeRemux    = "remux"
	TypeEncode   = "encode"
	TypeWEB      = "web"
	TypeHDTV     = "hdtv"
	Type3D       = "3d"
)

// Rules : ce qui change d'un tracker à l'autre.
type Rules struct {
	Tracker        string
	CatFilms       int
	CatFilmsVOSTFR int
	Types          map[string]int // genre → type_id
	Resolutions    map[string]int // « 1080p » → resolution_id
	// Banned : teams interdites (tag → raison). RawOnly : teams acceptées
	// seulement en brut (Remux, Full Disc).
	Banned  map[string]string
	RawOnly map[string]bool
	// BannedTokens : mentions du nom refusées (« HDLIGHT » → raison).
	BannedTokens map[string]string
	// Quality : règles d'upload et d'encodage (0.5.0 ; nil : aucune).
	Quality *Quality
}

// TypeID : l'identifiant du genre kind. Un WEB va au type « web » unique du
// tracker, sinon à « webdl » ou « webrip » selon web (webdl, webrip).
func (r Rules) TypeID(kind, web string) int {
	if kind != TypeWEB {
		return r.Types[kind]
	}
	if id := r.Types[TypeWEB]; id > 0 {
		return id
	}
	if id := r.Types[web]; id > 0 {
		return id
	}
	if id := r.Types["webdl"]; id > 0 {
		return id
	}
	return r.Types["webrip"]
}

// BannedToken : la mention refusée que porte ce nom, et pourquoi (sinon vide).
func BannedToken(name string, rules Rules) (string, string) {
	for _, t := range Parse(name).Tokens {
		if t.Kind == KTitle {
			continue // « TS » ou « Cam » peuvent faire partie d'un titre
		}
		for tok, why := range rules.BannedTokens {
			if strings.EqualFold(t.Value, tok) {
				return tok, why
			}
		}
	}
	return "", ""
}

// File : un fichier de la release.
type File struct {
	Rel  string `json:"rel"`
	Size int64  `json:"size"`
}

// Input : de quoi bâtir la fiche.
type Input struct {
	Name      string
	Files     []File
	MediaInfo string
	TMDB      int
	IMDB      int
	// Title : le titre de fiche en cours (sinon le nom de la release) ; c'est
	// lui que la conformité juge.
	Title string
	// TMDBTitle, TMDBYear : titre international (anglais) et année du film.
	TMDBTitle string
	TMDBYear  string
	// OrigLang : langue originale du film (code ISO 639-1 de TMDB) ;
	// Animation : film d'animation (seuils de débit propres).
	OrigLang  string
	Animation bool
	// MI : un MediaInfo déjà lu (ffprobe du Garde-manger) à la place du texte.
	MI *MediaInfo
	// Existing : ce que le tracker a déjà de ce film (et sous ce nom).
	Existing []tracker.Version
	Rules    Rules
}

// Fiche : la proposition, modifiable dans la page.
type Fiche struct {
	Release   Release        `json:"release"`
	MediaInfo MediaInfo      `json:"mediainfo"`
	TypeKind  string         `json:"typeKind"`
	Fields    tracker.Fields `json:"fields"`
	Reasons   []string       `json:"reasons"`  // d'où viennent les choix
	Warnings  []string       `json:"warnings"` // à vérifier, n'empêche pas
	Blockers  []string       `json:"blockers"` // empêche la publication
	// RuleBlockers : les blocages tirés des règles d'upload et d'encodage
	// (contenu du fichier), repris par le Garde-manger.
	RuleBlockers []string          `json:"-"`
	Suggested    string            `json:"suggested,omitempty"`
	Conform      []string          `json:"conform"` // écarts à la convention de nom
	TMDBQuery    string            `json:"tmdbQuery"`
	Year         string            `json:"year"`
	Versions     []tracker.Version `json:"versions"`
}

// Build prépare la fiche.
func Build(in Input) Fiche {
	// Le nom du fichier sert à chercher le film ; le titre de fiche en cours
	// (retouché ou adopté) fait foi pour tout le reste : type, langue, team,
	// doublon de nom, conformité.
	file := Parse(in.Name)
	rel := file
	if strings.TrimSpace(in.Title) != "" {
		rel = Parse(in.Title)
	}
	mi := ParseMediaInfo(in.MediaInfo)
	if in.MI != nil {
		mi = *in.MI
	}
	f := Fiche{Release: rel, MediaInfo: mi, TMDBQuery: file.Title, Year: file.Year, Reasons: []string{}, Warnings: []string{}, Blockers: []string{}}
	if file.Year != "" {
		f.TMDBQuery = file.Title + " " + file.Year
	}
	r := in.Rules
	f.Fields.Name = rel.Name
	f.Fields.Description = "[center][b]" + rel.Name + "[/b][/center]"
	f.Fields.MediaInfo = strings.TrimSpace(in.MediaInfo)
	f.Fields.TMDB, f.Fields.IMDB = in.TMDB, in.IMDB

	if rel.TV {
		f.Blockers = append(f.Blockers, "c'est une série : Baratie ne publie que des films pour l'instant")
	}

	// Type.
	kind, why := typeOf(rel, mi, in.Files)
	f.TypeKind = kind
	if kind == "" {
		f.Blockers = append(f.Blockers, "type indéterminé (ni WEB, ni BluRay, ni HDTV dans le nom) : choisis-le")
	} else {
		f.Fields.TypeID = r.TypeID(kind, webKind(rel, mi))
		f.Reasons = append(f.Reasons, why)
		if f.Fields.TypeID == 0 && len(r.Types) > 0 {
			f.Warnings = append(f.Warnings, "ce type n'a pas d'identifiant dans les règles de "+r.Tracker+" : choisis-le")
		}
	}
	if kind == TypeFullDisc {
		f.Blockers = append(f.Blockers, "Full Disc : un BDInfo est demandé, pas encore pris en charge par Baratie")
	}

	// Résolution : l'image fait foi, le nom sert à défaut.
	res := mi.Resolution()
	switch {
	case res != "" && rel.Res != "" && !strings.EqualFold(res, rel.Res):
		f.Warnings = append(f.Warnings, fmt.Sprintf("le nom dit %s, l'image fait %d×%d (%s) : vérifie la résolution", rel.Res, mi.Width, mi.Height, res))
		f.Reasons = append(f.Reasons, "résolution "+res+" d'après le MediaInfo")
	case res != "":
		f.Reasons = append(f.Reasons, fmt.Sprintf("résolution %s d'après le MediaInfo (%d×%d)", res, mi.Width, mi.Height))
	case rel.Res != "":
		res = rel.Res
		f.Reasons = append(f.Reasons, "résolution "+res+" d'après le nom")
	}
	if id, ok := r.Resolutions[res]; ok {
		f.Fields.ResolutionID = id
		f.Fields.SD = strings.HasPrefix(res, "576") || strings.HasPrefix(res, "480")
	} else {
		f.Blockers = append(f.Blockers, "résolution inconnue : choisis-la")
	}

	// Catégorie (langue).
	f.Fields.CategoryID = r.CatFilms
	vost := rel.Lang == "VOSTFR" || rel.Lang == "SUBFRENCH"
	if vost {
		f.Fields.CategoryID = r.CatFilmsVOSTFR
		f.Reasons = append(f.Reasons, "Films VOSTFR : "+rel.Lang+" dans le nom")
	}
	if mi.Empty && strings.TrimSpace(in.MediaInfo) == "" {
		f.Warnings = append(f.Warnings, "pas de MediaInfo : le tracker le demande (il est lu dans la page sur le plus gros fichier vidéo)")
	} else if mi.Empty {
		f.Warnings = append(f.Warnings, "le MediaInfo ne décrit aucune piste vidéo ni audio : vérifie-le avant d'envoyer")
	} else {
		fa, fs := mi.HasFrenchAudio(), mi.HasFrenchSubs()
		switch {
		case vost && fa:
			f.Warnings = append(f.Warnings, "le nom dit "+rel.Lang+" mais une piste audio est en français : vérifie la catégorie")
		case !vost && !fa && fs:
			f.Fields.CategoryID = r.CatFilmsVOSTFR
			f.Warnings = append(f.Warnings, "aucune piste audio en français, sous-titres français : rangé en Films VOSTFR")
		case !fa && !fs && r.Quality == nil:
			f.Warnings = append(f.Warnings, "ni piste audio ni sous-titre en français d'après le MediaInfo")
		}
		if rel.Lang == "MULTi" && len(mi.AudioLangs) < 2 {
			f.Warnings = append(f.Warnings, "MULTi dans le nom mais une seule piste audio")
		}
	}

	// Mentions refusées (HDLight…) : dans le titre comme dans le nom du fichier
	// (c'est le contenu qui compte, pas le titre qu'on lui donne).
	for _, n := range []string{rel.Name, file.Name} {
		if tok, why := BannedToken(n, r); tok != "" {
			f.Blockers = append(f.Blockers, tok+" refusé sur "+r.Tracker+" ("+why+")")
			break
		}
	}

	// Teams.
	g := strings.ToLower(rel.Group)
	for tag, why := range r.Banned {
		if g != "" && g == strings.ToLower(tag) {
			f.Blockers = append(f.Blockers, "team interdite sur "+r.Tracker+" : "+tag+" ("+why+")")
		}
	}
	for tag := range r.RawOnly {
		if g != "" && g == strings.ToLower(tag) && kind != TypeRemux && kind != TypeFullDisc {
			f.Blockers = append(f.Blockers, tag+" n'est accepté sur "+r.Tracker+" qu'en brut (Remux, Full Disc)")
		}
	}

	// TMDB.
	if in.TMDB == 0 {
		f.Blockers = append(f.Blockers, "choisis le film sur TMDB")
	}

	// Règles d'upload et d'encodage du tracker.
	nb := len(f.Blockers)
	checkQuality(&f, rel, mi, in, res)
	f.RuleBlockers = append([]string(nil), f.Blockers[nb:]...)
	if src := SourceLine(rel, kind, mi, res); src != "" {
		f.Fields.Description += "\n[center]Source : " + src + "[/center]"
		f.Reasons = append(f.Reasons, "source de l'encode citée dans la description : "+src)
	} else if web := WebLine(rel, kind, mi, res); web != "" {
		f.Fields.Description += "\n[center]Source : " + web + "[/center]"
		f.Reasons = append(f.Reasons, "plateforme, résolution, codec et profil du WEB-DL cités dans la description : "+web)
	}

	// Ce que le tracker a déjà (et la politique des doublons).
	local := sizesOf(in.Files)
	var total int64
	for _, s := range local {
		total += s
	}
	mine := KeyOf(rel, kind, mi, res)
	isVost := f.Fields.CategoryID == r.CatFilmsVOSTFR && r.CatFilmsVOSTFR != 0
	for _, v := range in.Existing {
		v.Same = v.Same || sameName(v.Name, rel.Name)
		v.Identical = len(v.Files) > 0 && sameSizes(v.Files, local)
		if !v.Identical && !v.Same && isVost == strings.Contains(strings.ToUpper(v.Category), "VOSTFR") {
			v.Dupe, v.DupeWhy = Compare(mine, total, v)
		}
		f.Versions = append(f.Versions, v)
		switch {
		case v.Identical:
			f.Blockers = append(f.Blockers, fmt.Sprintf("ta release est déjà sur %s à l'identique (mêmes fichiers) : %s", r.Tracker, v.Name))
		case v.Same:
			f.Blockers = append(f.Blockers, fmt.Sprintf("une release de ce nom est déjà sur %s", r.Tracker))
		case v.Dupe == DupeSame:
			f.Warnings = append(f.Warnings, fmt.Sprintf("doublon selon les règles de %s (%s) : %s ; seule une amélioration nette serait acceptée", r.Tracker, v.DupeWhy, v.Name))
		case v.Dupe == DupeMaybe:
			f.Warnings = append(f.Warnings, fmt.Sprintf("doublon possible (%s) : %s", v.DupeWhy, v.Name))
		}
	}

	// Convention de nom, jugée sur le titre de fiche en cours. Si le simple
	// réordonnancement ne suffit pas (nom maison : « Inception (2010).mkv »),
	// un titre est bâti avec TMDB et le MediaInfo.
	title := rel
	f.Conform, f.Suggested = Conformity(title)
	if len(f.Conform) > 0 && f.Suggested == "" {
		if p := Propose(title, mi, in.TMDBTitle, in.TMDBYear); !strings.EqualFold(p, title.Name) {
			f.Suggested = p
		}
	}
	return f
}

// typeOf : le genre de release et pourquoi.
func typeOf(rel Release, mi MediaInfo, files []File) (string, string) {
	disc, dvd := false, false
	for _, fl := range files {
		p := strings.ToUpper(strings.ReplaceAll(fl.Rel, `\`, "/"))
		switch {
		case strings.Contains(p, "BDMV/"):
			disc = true
		case strings.Contains(p, "VIDEO_TS/"):
			dvd = true
		case strings.EqualFold(path.Ext(p), ".ISO"):
			if rel.Source == "DVD" {
				dvd = true
			} else {
				disc = true
			}
		}
	}
	switch {
	case rel.ThreeD:
		return Type3D, "3D : « 3D » dans le nom"
	case disc:
		return TypeFullDisc, "Full Disc : structure de disque Blu-ray"
	case dvd:
		return TypeDVDR, "DVDR : structure de DVD"
	case rel.Remux:
		return TypeRemux, "Remux : « REMUX » dans le nom"
	case rel.Source == "WEB":
		return TypeWEB, "WEB : source web dans le nom"
	case rel.Source == "HDTV":
		return TypeHDTV, "HDTV : source télé dans le nom"
	case rel.Source == "BluRay" || rel.Source == "DVD" || rel.Source == "HDDVD":
		return TypeEncode, "Encode : source " + rel.Source + " réencodée"
	case mi.Library != "":
		return TypeEncode, "Encode : " + mi.Library + " d'après le MediaInfo (source absente du nom)"
	}
	return "", ""
}

func sizesOf(files []File) []int64 {
	out := make([]int64, 0, len(files))
	for _, f := range files {
		out = append(out, f.Size)
	}
	return out
}

// sameSizes : mêmes fichiers (multiensemble des tailles), en ignorant les
// petits fichiers (< 1 Mio : NFO, sample texte…) que les uns gardent et pas
// les autres.
func sameSizes(a, b []int64) bool {
	big := func(x []int64) []int64 {
		out := []int64{}
		for _, s := range x {
			if s >= 1<<20 {
				out = append(out, s)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	x, y := big(a), big(b)
	if len(x) == 0 || len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func sameName(a, b string) bool {
	n := func(s string) string {
		s = extRe.ReplaceAllString(strings.TrimSpace(s), "")
		return strings.ToLower(strings.Join(strings.FieldsFunc(s, func(c rune) bool { return c == '.' || c == ' ' || c == '_' }), "."))
	}
	return n(a) == n(b)
}
