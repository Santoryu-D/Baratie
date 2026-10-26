package fiche

// La politique des doublons courante sur UNIT3D. Une release est un
// doublon d'une autre quand elle a le même genre (Remux, Encode, WEB-DL,
// WEBRip…), la même source (plateforme pour le WEB : NF, AMZN… ; support
// sinon), la même résolution et la même famille de codec. Une plateforme
// différente n'est pas un doublon ; une amélioration nette (débit bien
// supérieur) est laissée au jugement du membre.

import (
	"fmt"
	"strings"

	"baratie/internal/tracker"
)

// Verdicts de doublon.
const (
	DupeSame  = "same"  // même source, résolution, codec : doublon
	DupeMaybe = "maybe" // probable (une info manque, ou ta version est bien plus lourde)
)

// GainRatio : au-delà de +25 % de taille (même durée, mêmes specs), ta
// version peut être une amélioration nette : à juger.
const GainRatio = 1.25

// Key : ce qui fait un doublon.
type Key struct {
	Kind   string // remux, encode, webdl, webrip, hdtv, fulldisc, dvdr, 3d
	Source string // NF, AMZN… pour le WEB (vide : inconnue) ; BluRay, DVD, HDTV sinon
	Res    string // 1080p…
	Codec  string // h264, h265, av1, vc1, mpeg2, xvid… (vide : inconnu)
}

func (k Key) String() string {
	parts := []string{}
	for _, s := range []string{kindLabel(k.Kind), k.Source, k.Res, codecLabel(k.Codec)} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

func kindLabel(k string) string {
	return map[string]string{"remux": "Remux", "encode": "Encode", "webdl": "WEB-DL", "webrip": "WEBRip", "hdtv": "HDTV", "fulldisc": "Full Disc", "dvdr": "DVDR", "3d": "3D"}[k]
}

func codecLabel(c string) string {
	return map[string]string{"h264": "H.264", "h265": "H.265", "av1": "AV1", "vc1": "VC-1", "mpeg2": "MPEG-2", "xvid": "XviD", "divx": "DivX", "vp9": "VP9"}[c]
}

// CodecFamily : x264, H264, AVC → h264 ; x265, H265, HEVC → h265…
func CodecFamily(c string) string {
	switch strings.ToLower(strings.NewReplacer(".", "", "-", "", " ", "").Replace(c)) {
	case "x264", "h264", "avc", "avc1":
		return "h264"
	case "x265", "h265", "hevc":
		return "h265"
	case "av1":
		return "av1"
	case "vc1":
		return "vc1"
	case "mpeg2", "mpegvideo":
		return "mpeg2"
	case "xvid":
		return "xvid"
	case "divx":
		return "divx"
	case "vp9":
		return "vp9"
	}
	return ""
}

// srcWord : le jeton source tel qu'écrit (web-dl, webrip, bluray…).
func srcWord(r Release) string {
	for _, t := range r.Tokens {
		if t.Kind == KSource {
			return strings.ToLower(strings.ReplaceAll(t.Value, "-", ""))
		}
	}
	return ""
}

// webKind : WEB-DL (intact) ou WEBRip (réencodé).
func webKind(r Release, mi MediaInfo) string {
	switch srcWord(r) {
	case "webrip":
		return "webrip"
	case "webdl":
		return "webdl"
	}
	// « WEB » seul : un x264/x265 lu dans le MediaInfo, ou un codec écrit
	// « x264 » dans le nom (convention scène), dit l'encode.
	if mi.Library != "" || strings.HasPrefix(strings.ToLower(r.Codec), "x26") {
		return "webrip"
	}
	return "webdl"
}

// KeyOf : la clé d'une release (kind : le genre de la fiche, ou celui du
// tracker pour une version existante).
func KeyOf(r Release, kind string, mi MediaInfo, res string) Key {
	k := Key{Res: strings.ToLower(res), Codec: CodecFamily(r.Codec)}
	if mi.VideoFmt != "" {
		if c := CodecFamily(mi.VideoFmt); c != "" {
			k.Codec = c
		}
		if strings.Contains(strings.ToLower(mi.VideoFmt), "mpeg-4 visual") {
			k.Codec = "xvid"
		}
	}
	if k.Res == "" {
		k.Res = strings.ToLower(r.Res)
	}
	switch kind {
	case TypeWEB:
		k.Kind = webKind(r, mi)
		k.Source = r.Provider
	case TypeEncode:
		k.Kind, k.Source = "encode", r.Source
	default:
		k.Kind, k.Source = kind, r.Source
		if kind == TypeRemux || kind == TypeFullDisc {
			k.Source = "BluRay"
			if r.Source == "DVD" {
				k.Source = "DVD"
			}
		}
	}
	return k
}

// kindOfType : le genre d'une version d'après le type affiché par le tracker.
func kindOfType(t string) string {
	switch strings.ToLower(strings.ReplaceAll(t, " ", "")) {
	case "remux":
		return TypeRemux
	case "encode":
		return TypeEncode
	case "web", "webdl", "web-dl", "webrip":
		return TypeWEB
	case "hdtv":
		return TypeHDTV
	case "fulldisc":
		return TypeFullDisc
	case "dvdr":
		return TypeDVDR
	case "3d":
		return Type3D
	}
	return ""
}

// KindOf : le genre d'une release d'après son seul nom.
func KindOf(r Release) string {
	k, _ := typeOf(r, MediaInfo{}, nil)
	return k
}

// VersionKey : la clé d'une version déjà sur le tracker (d'après son nom et
// son type).
func VersionKey(v tracker.Version) Key {
	r := Parse(v.Name)
	kind := kindOfType(v.Type)
	if kind == "" {
		kind, _ = typeOf(r, MediaInfo{}, nil)
	}
	res := v.Resolution
	if res == "" {
		res = r.Res
	}
	return KeyOf(r, kind, MediaInfo{}, res)
}

// Compare : ta clé face à celle d'une version. Rend le verdict et pourquoi.
func Compare(mine Key, mySize int64, v tracker.Version) (string, string) {
	vk := VersionKey(v)
	if mine.Kind == "" || vk.Kind == "" || mine.Kind != vk.Kind || mine.Res == "" || !strings.EqualFold(mine.Res, vk.Res) {
		return "", ""
	}
	// Supports : deux sources connues et différentes ne sont pas des doublons.
	if mine.Source != "" && vk.Source != "" && !strings.EqualFold(mine.Source, vk.Source) {
		return "", ""
	}
	if mine.Codec != "" && vk.Codec != "" && mine.Codec != vk.Codec {
		return "", ""
	}
	gain := ""
	if mySize > 0 && v.Size > 0 && float64(mySize) >= float64(v.Size)*GainRatio {
		gain = fmt.Sprintf("ta version est %d %% plus lourde : amélioration nette ? à toi de juger", int((float64(mySize)/float64(v.Size)-1)*100+0.5))
	}
	unknown := []string{}
	if mine.Source == "" || vk.Source == "" {
		unknown = append(unknown, "plateforme")
	}
	if mine.Codec == "" || vk.Codec == "" {
		unknown = append(unknown, "codec")
	}
	switch {
	case len(unknown) > 0:
		why := "même genre et même résolution (" + vk.String() + "), " + strings.Join(unknown, " et ") + " à vérifier"
		if gain != "" {
			why += " ; " + gain
		}
		return DupeMaybe, why
	case gain != "":
		return DupeMaybe, "mêmes specs (" + vk.String() + ") ; " + gain
	}
	return DupeSame, "mêmes specs (" + vk.String() + ")"
}
