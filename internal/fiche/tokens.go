// Package fiche prépare la fiche d'un film à publier : ce que dit le nom de
// la release, ce que dit le MediaInfo, ce qui en découle (type, résolution,
// langue), les garde-fous (teams interdites, doublons) et la conformité du nom
// à la convention du tracker.
//
// Rien ici ne parle au réseau : tout se teste sur des noms et des MediaInfo.
package fiche

import (
	"path"
	"regexp"
	"strings"

	"baratie/internal/names"
)

// Genre d'un jeton du nom.
const (
	KTitle    = "title"
	KYear     = "year"
	KLang     = "lang"     // MULTi, FRENCH, TRUEFRENCH, VOSTFR, SUBFRENCH
	KLangVar  = "langvar"  // VFF, VFQ, VFI, VF2, VOF
	KRes      = "res"      // 1080p…
	KProvider = "provider" // NF, AMZN… (avant WEB)
	KSource   = "source"   // WEB, WEB-DL, WEBRip, BluRay, HDTV…
	KRemux    = "remux"
	KHDR      = "hdr"
	KAudio    = "audio"
	KCodec    = "codec"
	KTag      = "tag" // REPACK, PROPER, EXTENDED, iNTERNAL…
	K3D       = "3d"
	KOther    = "other"
)

// Token : un morceau du nom.
type Token struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Release : ce que dit le nom.
type Release struct {
	Name       string  `json:"name"`
	Title      string  `json:"title"`
	Year       string  `json:"year"`
	Lang       string  `json:"lang"`    // langue principale (MULTi, FRENCH…)
	LangVar    string  `json:"langVar"` // VFF, VFQ…
	Res        string  `json:"res"`
	Source     string  `json:"source"` // WEB, BluRay, HDTV, DVD… (normalisé)
	Provider   string  `json:"provider,omitempty"`
	Remux      bool    `json:"remux"`
	Codec      string  `json:"codec"`
	HDR        string  `json:"hdr,omitempty"`
	ThreeD     bool    `json:"threeD"`
	Group      string  `json:"group"`
	TV         bool    `json:"tv"`
	Tokens     []Token `json:"tokens"`
	titleWords int     // nombre de mots du titre en tête des jetons
}

var (
	extRe     = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m2ts|ts|iso|m4v|wmv|mov|webm)$`)
	yearRe    = regexp.MustCompile(`^(19|20)\d{2}$`)
	resRe     = regexp.MustCompile(`(?i)^(4320|2160|1080|720|576|480)([pi])$`)
	chanRe    = regexp.MustCompile(`^\d\.\d$`)
	audioRe   = regexp.MustCompile(`(?i)^(dd\+?|ddp|ddp?\d\.\d|dd\+?\d\.\d|e-?ac-?3|eac3\d\.\d|ac-?3|ac3\d\.\d|truehd|dts(-?hd)?(-?ma|-?hra)?|dts-?x|dts-?es|dts:x|aac|aac\d\.\d|flac|opus|lpcm|pcm|mp3|atmos|auro3d|ma)$`)
	codecRe   = regexp.MustCompile(`(?i)^(x264|x265|h\.?264|h\.?265|hevc|avc|av1|vc-?1|mpeg-?2|xvid|divx|vp9)$`)
	hdrRe     = regexp.MustCompile(`(?i)^(hdr|hdr10|hdr10\+|hdr10plus|dv|dovi|hlg|pq10|10bits?|hi10p)$`)
	langRe    = regexp.MustCompile(`(?i)^(multi|multi2|french|truefrench|vostfr|subfrench|vost|english|vo)$`)
	langVarRe = regexp.MustCompile(`(?i)^(vff|vfq|vfi|vf2|vof|vfb)$`)
	yearAnyRe = regexp.MustCompile(`[ ._(-]((?:19|20)\d{2})(?:[ ._)-]|$)`)
	radarrQ   = regexp.MustCompile(`(?i)^(bluray|webdl|web-dl|webrip|remux|hdtv|dvd|sdtv|bdremux)-(\d{3,4}p)$`)
	seasonRe  = regexp.MustCompile(`(?i)(^|[ ._-])(s\d{1,2}(e\d{1,3})*|saison|season)([ ._-]|$)`)
	editionRe = regexp.MustCompile(`(?i)[ ._-]version[ ._-](integrale|intégrale|longue|complete)([ ._-]|$)`)
	tagRe     = regexp.MustCompile(`(?i)^(integrale|intégrale|repack\d?|proper\d?|rerip\d?|internal|extended|unrated|uncut|imax|remastered|directors|director's|cut|dc|theatrical|custom|limited|hybrid|open\.?matte)$`)
	srcRe     = regexp.MustCompile(`(?i)^(web|web-?dl|webrip|bluray|blu-ray|bdrip|brrip|bdremux|hdtv|uhdtv|dvdrip|dvd|dvdr|dvd5|dvd9|hddvd|hd-dvd|uhd|tvrip|hdrip|hdlight|4klight|mhd)$`)
	provRe    = regexp.MustCompile(`(?i)^(nf|amzn|dsnp|atvp|hmax|max|pcok|hulu|cr|adn|pmtp|stan|crav|itunes|ma|mycanal|cnlp|arte|tf1|m6|frtv|ftv|salto|ovid|vrv|bcore|hbo)$`)
)

// Parse lit un nom de release.
func Parse(name string) Release {
	name = strings.TrimSpace(name)
	base := extRe.ReplaceAllString(path.Base(strings.ReplaceAll(name, `\`, "/")), "")
	r := Release{Name: base}
	nr := names.Parse(base)
	r.Title, r.Year, r.TV = nr.Title, nr.Year, nr.TV
	// Année manquée (le lecteur de noms s'arrête à « Intégrale ») : la
	// dernière année plausible du nom.
	if r.Year == "" {
		if m := yearAnyRe.FindAllStringSubmatch(base, -1); len(m) > 0 {
			r.Year = m[len(m)-1][1]
		}
	}
	// « Intégrale » / « Complete » seuls ne font pas une série : « Version
	// Intégrale » est le montage long d'un film. Série = un épisode, une
	// saison, ou une intégrale sans année.
	if r.TV && !seasonRe.MatchString(base) && r.Year != "" {
		r.TV = false
	}
	if t := strings.TrimSuffix(r.Title, " Version"); t != r.Title && editionRe.MatchString(base) {
		r.Title = t
	}

	body := base
	// Team : après le dernier tiret, si c'est un mot simple.
	if i := strings.LastIndex(body, "-"); i > 0 && i < len(body)-1 {
		g := body[i+1:]
		if !strings.ContainsAny(g, ". _") && !resRe.MatchString(g) && !strings.EqualFold(g, "DL") && !strings.EqualFold(g, "Rip") {
			r.Group = g
			body = body[:i]
		}
	}
	parts := strings.FieldsFunc(body, func(c rune) bool { return c == '.' || c == ' ' || c == '_' })
	// Les canaux « 5.1 » sont coupés par le point : on les recolle
	// (« DDP5 » « 1 », « DD+ » « 5 » « 1 »).
	var words []string
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if i+1 < len(parts) && len(parts[i+1]) == 1 && parts[i+1][0] >= '0' && parts[i+1][0] <= '2' && len(p) > 0 && p[len(p)-1] >= '1' && p[len(p)-1] <= '9' {
			if chanRe.MatchString(p[len(p)-1:]+"."+parts[i+1]) && (len(p) == 1 || audioRe.MatchString(p+"."+parts[i+1])) {
				words = append(words, p+"."+parts[i+1])
				i++
				continue
			}
		}
		// Qualité à la Radarr : « Bluray-1080p », « WEBDL-2160p », « Remux-1080p ».
		if m := radarrQ.FindStringSubmatch(p); m != nil {
			words = append(words, m[1], m[2])
			continue
		}
		words = append(words, p)
	}

	// Le titre : les premiers mots, jusqu'à l'année (la dernière plausible) ou
	// au premier jeton technique.
	yearIdx := -1
	for i, w := range words {
		if yearRe.MatchString(w) && i > 0 && w == r.Year {
			yearIdx = i
		}
	}
	inTitle := true
	for i, w := range words {
		k := classify(w)
		if inTitle {
			if (yearIdx >= 0 && i < yearIdx) || (yearIdx < 0 && k == KOther) {
				r.Tokens = append(r.Tokens, Token{KTitle, w})
				r.titleWords++
				continue
			}
			inTitle = false
		}
		if i == yearIdx {
			k = KYear
		}
		r.Tokens = append(r.Tokens, Token{k, w})
		switch k {
		case KLang:
			if r.Lang == "" {
				r.Lang = normLang(w)
			}
		case KLangVar:
			if r.LangVar == "" {
				r.LangVar = strings.ToUpper(w)
			}
		case KRes:
			r.Res = strings.ToLower(w)
		case KSource:
			if s := normSource(w); s != "" && r.Source == "" {
				r.Source = s
			}
			if strings.EqualFold(w, "bdremux") {
				r.Remux = true
			}
		case KProvider:
			r.Provider = strings.ToUpper(w)
		case KRemux:
			r.Remux = true
		case KCodec:
			r.Codec = normCodec(w)
		case KHDR:
			if r.HDR == "" {
				r.HDR = strings.ToUpper(w)
			}
		case K3D:
			r.ThreeD = true
		}
	}
	// « UHD » seul dit la source Blu-ray UHD quand rien d'autre ne la donne.
	return r
}

func classify(w string) string {
	switch {
	case yearRe.MatchString(w):
		return KYear
	case resRe.MatchString(w):
		return KRes
	case strings.EqualFold(w, "remux"):
		return KRemux
	case strings.EqualFold(w, "3d"):
		return K3D
	case langRe.MatchString(w):
		return KLang
	case langVarRe.MatchString(w):
		return KLangVar
	case srcRe.MatchString(w):
		return KSource
	case codecRe.MatchString(w):
		return KCodec
	case hdrRe.MatchString(w):
		return KHDR
	case audioRe.MatchString(w) || chanRe.MatchString(w):
		return KAudio
	case tagRe.MatchString(w):
		return KTag
	case provRe.MatchString(w):
		return KProvider
	}
	return KOther
}

func normLang(w string) string {
	switch strings.ToLower(w) {
	case "multi", "multi2":
		return "MULTi"
	case "french":
		return "FRENCH"
	case "truefrench":
		return "TRUEFRENCH"
	case "vostfr", "vost":
		return "VOSTFR"
	case "subfrench":
		return "SUBFRENCH"
	case "english":
		return "ENGLISH"
	case "vo":
		return "VO"
	}
	return strings.ToUpper(w)
}

// normSource : WEB, BluRay, HDTV, DVD (ou "" pour un jeton sans source claire).
func normSource(w string) string {
	switch strings.ToLower(strings.ReplaceAll(w, "-", "")) {
	case "web", "webdl", "webrip":
		return "WEB"
	case "bluray", "bdrip", "brrip", "bdremux", "uhd", "hdlight", "4klight", "mhd":
		return "BluRay"
	case "hdtv", "uhdtv", "tvrip":
		return "HDTV"
	case "dvdrip", "dvd", "dvdr", "dvd5", "dvd9":
		return "DVD"
	case "hddvd":
		return "HDDVD"
	}
	return ""
}

func normCodec(w string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(w, ".", ""), "-", "")) {
	case "x264":
		return "x264"
	case "x265":
		return "x265"
	case "h264":
		return "H264"
	case "h265":
		return "H265"
	case "hevc":
		return "HEVC"
	case "avc":
		return "AVC"
	case "av1":
		return "AV1"
	case "vc1":
		return "VC-1"
	case "mpeg2":
		return "MPEG-2"
	case "xvid":
		return "XviD"
	case "divx":
		return "DivX"
	case "vp9":
		return "VP9"
	}
	return w
}
