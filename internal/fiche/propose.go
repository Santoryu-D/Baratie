package fiche

import (
	"strings"
	"unicode"
)

// sansAccent : les lettres accentuées courantes et leur base.
var sansAccent = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "å", "a", "À", "A", "Â", "A", "Ä", "A", "Á", "A",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "É", "E", "È", "E", "Ê", "E", "Ë", "E",
	"î", "i", "ï", "i", "í", "i", "ì", "i", "Î", "I", "Ï", "I", "Í", "I",
	"ô", "o", "ö", "o", "ó", "o", "ò", "o", "õ", "o", "ø", "o", "Ô", "O", "Ö", "O", "Ó", "O", "Ø", "O",
	"û", "u", "ü", "u", "ú", "u", "ù", "u", "Û", "U", "Ü", "U", "Ú", "U", "Ù", "U",
	"ç", "c", "Ç", "C", "ñ", "n", "Ñ", "N", "ÿ", "y", "ý", "y",
	"æ", "ae", "Æ", "AE", "œ", "oe", "Œ", "OE", "ß", "ss",
)

// Placeholders : ce que ni le nom ni le MediaInfo ne disent de façon sûre.
const (
	MissingSource = "[SOURCE]"
	MissingTeam   = "[TEAM]"
)

// Propose bâtit un titre conforme (TITRE.ANNEE.LANGUE.RESOLUTION.SOURCE.CODEC-TEAM)
// avec ce qu'on sait : le titre international et l'année de TMDB, ce que dit
// le nom, et ce que mesure le MediaInfo (langue d'après les pistes,
// résolution, HDR, codec). Ce qu'aucun ne dit (source, team) reste à
// compléter : [SOURCE], [TEAM].
func Propose(r Release, mi MediaInfo, tmdbTitle, tmdbYear string) string {
	title := dotted(tmdbTitle)
	if title == "" {
		title = dotted(r.Title)
	}
	year := tmdbYear
	if year == "" {
		year = r.Year
	}
	parts := []string{title}
	if year != "" {
		parts = append(parts, year)
	}
	for _, t := range r.Tokens {
		if t.Kind == K3D || t.Kind == KTag {
			parts = append(parts, t.Value)
		}
	}
	// Langue : celle du nom, sinon celle des pistes.
	lang := r.Lang
	if lang == "" && !mi.Empty {
		fa, fs := mi.HasFrenchAudio(), mi.HasFrenchSubs()
		switch {
		case fa && len(mi.AudioLangs) > 1:
			lang = "MULTi"
		case fa:
			lang = "FRENCH"
		case fs:
			lang = "VOSTFR"
		}
	}
	if lang != "" {
		parts = append(parts, lang)
	}
	if r.LangVar != "" {
		parts = append(parts, r.LangVar)
	}
	res := mi.Resolution()
	if res == "" {
		res = r.Res
	}
	if res != "" {
		parts = append(parts, res)
	}
	src := pick(r, KProvider, KSource)
	if len(src) == 0 {
		src = []string{MissingSource}
	}
	parts = append(parts, src...)
	if r.Remux {
		parts = append(parts, "REMUX")
	}
	hdr := pick(r, KHDR)
	if len(hdr) == 0 {
		if h := hdrOf(mi.HDR); h != "" {
			hdr = []string{h}
		}
	}
	parts = append(parts, hdr...)
	parts = append(parts, pick(r, KAudio)...)
	codec := r.Codec
	if codec == "" {
		codec = codecOf(r, mi)
	}
	if codec != "" {
		parts = append(parts, codec)
	}
	team := r.Group
	if team == "" {
		team = MissingTeam
	}
	return strings.Join(parts, ".") + "-" + team
}

func pick(r Release, kinds ...string) []string {
	var v []string
	for _, t := range r.Tokens {
		for _, k := range kinds {
			if t.Kind == k {
				v = append(v, t.Value)
			}
		}
	}
	return v
}

// hdrOf : le format HDR du MediaInfo en mention de nom.
func hdrOf(f string) string {
	l := strings.ToLower(f)
	switch {
	case l == "":
		return ""
	case strings.Contains(l, "dolby vision") && (strings.Contains(l, "hdr10+") || strings.Contains(l, "2094")):
		return "DV.HDR10+"
	case strings.Contains(l, "dolby vision") && (strings.Contains(l, "hdr10") || strings.Contains(l, "2086")):
		return "DV.HDR"
	case strings.Contains(l, "dolby vision"):
		return "DV"
	case strings.Contains(l, "hdr10+") || strings.Contains(l, "2094"):
		return "HDR10+"
	case strings.Contains(l, "hlg"):
		return "HLG"
	case strings.Contains(l, "hdr") || strings.Contains(l, "2086"):
		return "HDR"
	}
	return ""
}

// codecOf : x264/x265 pour un encode (lu dans le MediaInfo), sinon la norme
// (H264/H265 pour un WEB, AVC/HEVC pour un disque ou un remux).
func codecOf(r Release, mi MediaInfo) string {
	if mi.Library != "" {
		return mi.Library
	}
	f := strings.ToUpper(mi.VideoFmt)
	disc := r.Remux || r.Source == "BluRay"
	switch {
	case f == "AVC" && disc:
		return "AVC"
	case f == "AVC":
		return "H264"
	case f == "HEVC" && disc:
		return "HEVC"
	case f == "HEVC":
		return "H265"
	case f == "AV1":
		return "AV1"
	case strings.HasPrefix(f, "MPEG VIDEO"):
		return "MPEG-2"
	case f == "VC-1":
		return "VC-1"
	}
	return ""
}

// dotted : « Le Fabuleux Destin d'Amélie Poulain » → « Le.Fabuleux.Destin.d.Amelie.Poulain ».
func dotted(s string) string {
	s = sansAccent.Replace(s)
	var b strings.Builder
	sep := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
			// accent combinant : retiré
		case r == '&':
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString("and")
			sep = true
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-':
			if sep && b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteRune(r)
			sep = false
		default:
			sep = true
		}
	}
	return b.String()
}
