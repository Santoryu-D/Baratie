package fiche

// Baratie 0.5.0 : les règles d'upload et d'encodage du tracker, appliquées
// au MediaInfo. Bloquant quand la règle est claire et la mesure fiable ;
// alerte quand la règle laisse une marge ou que la mesure est incertaine.

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Quality : les seuils et listes du tracker (nil : rien n'est vérifié).
type Quality struct {
	// VONeedsFrenchSubs : une VO doit avoir des sous-titres français ;
	// MultiNeedsOriginal : une release MULTi doit avoir la langue originale.
	VONeedsFrenchSubs  bool
	MultiNeedsOriginal bool
	// MinKbps : débit vidéo minimum d'un encode, clé « bluray/x264/1080p »
	// ou « web/x265/720p » ; MinKbpsAnime pour l'animation.
	MinKbps      map[string]int
	MinKbpsAnime map[string]int
	// SD (DVDRip, BDRip SD) : débit vidéo minimum, AC3 maximum.
	SDMinKbps int
	SDMaxAC3  int
	// Encodes Blu-ray HD : débit audio minimum conseillé.
	HDMinAudioKbps int
}

// Genres d'encode pour les seuils.
const (
	encNone   = ""
	encBluRay = "bluray"
	encWEB    = "web"
	encSD     = "sd"
)

var (
	rcRe        = regexp.MustCompile(`(?:^|[\s/])rc=([a-z0-9]+)`)
	multiPassRe = regexp.MustCompile(`(?:^|[\s/])(stats-read=[1-9]|pass=[2-9])`)
	gpuRe       = regexp.MustCompile(`(?i)nvenc|nvencc|\bqsv\b|quick ?sync|\bamf\b|\bvce\b|videotoolbox|vaapi`)
)

// encodeKind : quel tableau de seuils s'applique.
func encodeKind(rel Release, kind string, mi MediaInfo, res string) string {
	sd := strings.HasPrefix(res, "576") || strings.HasPrefix(res, "480")
	switch kind {
	case TypeEncode:
		if sd || rel.Source == "DVD" {
			return encSD
		}
		return encBluRay
	case TypeWEB:
		if webKind(rel, mi) == "webrip" && !sd {
			return encWEB
		}
	}
	return encNone
}

// encCodec : x264, x265 ou av1 (pour les seuils).
func encCodec(mi MediaInfo, rel Release) string {
	if mi.Library != "" {
		return mi.Library
	}
	switch CodecFamily(mi.VideoFmt) {
	case "h264":
		return "x264"
	case "h265":
		return "x265"
	case "av1":
		return "av1"
	}
	switch CodecFamily(rel.Codec) {
	case "h264":
		return "x264"
	case "h265":
		return "x265"
	case "av1":
		return "av1"
	}
	return ""
}

func resBucket(res string) string {
	switch {
	case strings.HasPrefix(res, "2160"):
		return "2160p"
	case strings.HasPrefix(res, "1080"):
		return "1080p"
	case strings.HasPrefix(res, "720"):
		return "720p"
	}
	return ""
}

// Noms anglais des langues (MediaInfo) d'après le code ISO 639-1 de TMDB.
var langNames = map[string][]string{
	"fr": {"french"}, "en": {"english"}, "ja": {"japanese"}, "it": {"italian"}, "es": {"spanish", "castilian"}, "de": {"german"},
	"ko": {"korean"}, "zh": {"chinese", "mandarin"}, "cn": {"cantonese", "chinese"}, "hi": {"hindi"},
	"ru": {"russian"}, "pt": {"portuguese"}, "sv": {"swedish"}, "da": {"danish"}, "no": {"norwegian"},
	"nb": {"norwegian"}, "nl": {"dutch", "flemish"}, "pl": {"polish"}, "tr": {"turkish"}, "th": {"thai"},
	"fi": {"finnish"}, "el": {"greek"}, "he": {"hebrew"}, "ar": {"arabic"}, "cs": {"czech"},
	"hu": {"hungarian"}, "ro": {"romanian"}, "fa": {"persian", "farsi"}, "id": {"indonesian"},
	"ta": {"tamil"}, "te": {"telugu"}, "is": {"icelandic"}, "uk": {"ukrainian"}, "sr": {"serbian"},
	"hr": {"croatian"}, "bn": {"bengali"}, "ml": {"malayalam"}, "tl": {"tagalog", "filipino"}, "vi": {"vietnamese"},
}

// hasLang : une piste est-elle dans la langue (code ISO 639-1) ?
func hasLang(langs []string, code string) bool {
	names := langNames[code]
	for _, l := range langs {
		ll := strings.ToLower(strings.TrimSpace(l))
		if ll == code || strings.HasPrefix(ll, code+"-") {
			return true
		}
		for _, n := range names {
			if strings.HasPrefix(ll, n) {
				return true
			}
		}
	}
	return false
}

func anyKnown(langs []string) bool {
	for _, l := range langs {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

func isMP3(a Audio) bool {
	return strings.EqualFold(a.Format, "MPEG Audio") && strings.Contains(strings.ToLower(a.Profile), "layer 3") || strings.EqualFold(a.Format, "MP3")
}

func isAC3(a Audio) bool { return strings.EqualFold(a.Format, "AC-3") }

func isAAC(a Audio) bool { return strings.EqualFold(a.Format, "AAC") }

func isPCM(a Audio) bool {
	return strings.EqualFold(a.Format, "PCM") || strings.EqualFold(a.Format, "LPCM")
}

// Formats audio de la liste du tracker (DTS-HD, DTS, AC-3, E-AC-3, TrueHD,
// AAC, FLAC) ; MediaInfo écrit TrueHD « MLP FBA ».
func audioListed(a Audio) bool {
	f := strings.ToLower(a.Format)
	for _, ok := range []string{"dts", "ac-3", "e-ac-3", "mlp fba", "truehd", "aac", "flac"} {
		if strings.HasPrefix(f, ok) {
			return true
		}
	}
	return false
}

// Container : Matroska, MPEG-4, AVI, MPEG-TS… (MediaInfo, sinon extension).
func container(mi MediaInfo, files []File) string {
	if mi.Container != "" {
		return mi.Container
	}
	var big File
	for _, f := range files {
		if f.Size > big.Size {
			big = f
		}
	}
	switch strings.ToLower(path.Ext(big.Rel)) {
	case ".mkv":
		return "Matroska"
	case ".mp4", ".m4v":
		return "MPEG-4"
	case ".avi":
		return "AVI"
	case ".ts", ".m2ts":
		return "MPEG-TS"
	}
	return ""
}

// checkQuality ajoute blocages et alertes d'après les règles du tracker.
func checkQuality(f *Fiche, rel Release, mi MediaInfo, in Input, res string) {
	q := in.Rules.Quality
	if q == nil {
		return
	}
	trk := in.Rules.Tracker
	block := func(s string, a ...interface{}) { f.Blockers = append(f.Blockers, fmt.Sprintf(s, a...)) }
	warn := func(s string, a ...interface{}) { f.Warnings = append(f.Warnings, fmt.Sprintf(s, a...)) }
	kind := f.TypeKind
	ek := encodeKind(rel, kind, mi, res)
	dv := strings.Contains(strings.ToLower(mi.HDR), "dolby vision") || strings.EqualFold(rel.HDR, "DV") || strings.EqualFold(rel.HDR, "DOVI")

	// Fichiers en trop : sample, captures, sous-titres externes.
	var extra, subsOut []string
	for _, fl := range in.Files {
		base := strings.ToLower(path.Base(strings.ReplaceAll(fl.Rel, `\`, "/")))
		ext := path.Ext(base)
		switch {
		case ext == ".srt" || ext == ".sub" || ext == ".idx" || ext == ".ass" || ext == ".ssa" || ext == ".sup":
			subsOut = append(subsOut, path.Base(fl.Rel))
		case ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".bmp" || ext == ".gif":
			extra = append(extra, path.Base(fl.Rel))
		case strings.Contains(base, "sample") && (ext == ".mkv" || ext == ".mp4" || ext == ".avi" || ext == ".m2ts" || ext == ".ts"):
			extra = append(extra, path.Base(fl.Rel))
		}
	}
	if len(extra) > 0 {
		block("fichiers interdits sur %s (sample, captures) : %s ; publie le film seul", trk, strings.Join(extra, ", "))
	}
	if len(subsOut) > 0 {
		if dv {
			warn("sous-titres dans des fichiers séparés (%s) : tolérés seulement pour le Dolby Vision", strings.Join(subsOut, ", "))
		} else {
			block("sous-titres dans des fichiers séparés (%s) : interdits sur %s (sauf Dolby Vision), ils doivent être dans le MKV", strings.Join(subsOut, ", "), trk)
		}
	}

	if mi.Empty {
		return
	}

	// Langues : VO sans sous-titres français, MULTi sans la VO.
	if q.VONeedsFrenchSubs && anyKnown(mi.AudioLangs) && !mi.HasFrenchAudio() && !mi.HasFrenchSubs() {
		block("VO sans sous-titres français : interdit sur %s", trk)
	}
	if q.MultiNeedsOriginal && rel.Lang == "MULTi" && in.OrigLang != "" && in.OrigLang != "fr" {
		switch {
		case langNames[in.OrigLang] == nil:
		case !anyKnown(mi.AudioLangs):
			warn("MULTi : la langue originale du film (%s) doit être présente ; les pistes audio n'indiquent pas leur langue, vérifie", in.OrigLang)
		case !hasLang(mi.AudioLangs, in.OrigLang):
			block("MULTi sans la langue originale du film (%s) : %s l'exige", strings.Title(langNames[in.OrigLang][0]), trk)
		}
	}

	// Conteneur.
	switch c := strings.ToLower(container(mi, in.Files)); {
	case c == "" || c == "matroska":
	case c == "mpeg-4":
		if !dv {
			block("conteneur MP4 : %s demande du MKV (le MP4 n'est permis que pour le Dolby Vision)", trk)
		}
	case c == "mpeg-ts" || c == "bdav":
		if kind != TypeHDTV {
			block("conteneur %s : %s demande du MKV (le .ts n'est permis que pour le HDTV)", mi.Container, trk)
		}
	case c == "avi":
		if ek == encSD {
			warn("conteneur AVI : les règles d'encodage de %s acceptent le XviD des DVDRip, mais celles d'upload demandent du MKV ; à confirmer avec le Helpdesk", trk)
		} else {
			block("conteneur AVI : %s demande du MKV", trk)
		}
	default:
		block("conteneur %s : %s demande du MKV", mi.Container, trk)
	}

	// Codec vidéo.
	vf := strings.ToLower(mi.VideoFmt)
	xvid := strings.Contains(vf, "mpeg-4 visual")
	divx := xvid && (strings.HasPrefix(strings.ToUpper(mi.VideoCodec), "DX") || strings.HasPrefix(strings.ToUpper(mi.VideoCodec), "DIV"))
	switch {
	case vf == "":
	case xvid:
		switch {
		case ek != encSD:
			block("codec XviD/DivX : %s ne l'accepte que pour les DVDRip et BDRip SD", trk)
		case divx:
			warn("codec DivX : seulement « toléré dans certains cas » sur %s (XviD et x264 sont la règle)", trk)
		}
	case strings.HasPrefix(vf, "avc"), strings.HasPrefix(vf, "hevc"), strings.HasPrefix(vf, "av1"), strings.HasPrefix(vf, "vc-1"),
		strings.HasPrefix(vf, "vp9"), strings.HasPrefix(vf, "mpeg video"), strings.HasPrefix(vf, "jpeg 2000"):
		if ek == encSD && !strings.HasPrefix(vf, "avc") {
			block("codec %s : %s n'accepte que XviD et x264 pour les DVDRip et BDRip SD", mi.VideoFmt, trk)
		}
	default:
		block("codec vidéo %s : absent de la liste de %s", mi.VideoFmt, trk)
	}

	// Encodeur.
	if m := gpuRe.FindString(mi.LibraryRaw + " " + mi.Application + " " + mi.Settings); m != "" {
		block("encodage GPU (%s) : interdit sur %s", m, trk)
	}
	if ek == encBluRay || ek == encWEB {
		isAVC, isHEVC := strings.HasPrefix(vf, "avc"), strings.HasPrefix(vf, "hevc")
		switch {
		case (isAVC || isHEVC) && mi.Library == "" && mi.LibraryRaw != "":
			block("encodeur %s : %s n'accepte que x264, x265 (et AV1)", mi.LibraryRaw, trk)
		case (isAVC || isHEVC) && mi.Library == "":
			warn("encodeur inconnu (pas de « Writing library » dans le MediaInfo) : %s n'accepte que x264, x265 et AV1", trk)
		}
		if m := rcRe.FindStringSubmatch(mi.Settings); m != nil {
			rc := m[1]
			if rc != "crf" && rc != "2pass" && !multiPassRe.MatchString(mi.Settings) {
				block("encodage en mode %s : %s n'accepte que le CRF ou le multi-passe", rc, trk)
			}
		}
	}

	// Débit vidéo minimum.
	if ek == encBluRay || ek == encWEB {
		key := ek + "/" + encCodec(mi, rel) + "/" + resBucket(res)
		table, label := q.MinKbps, ""
		if in.Animation {
			table, label = q.MinKbpsAnime, " (animation)"
		}
		if min := table[key]; min > 0 {
			switch {
			case mi.VideoKbps == 0:
				warn("débit vidéo introuvable dans le MediaInfo : le minimum de %s est %d kb/s ici%s", trk, min, label)
			case mi.VideoKbps < min:
				block("débit vidéo de %d kb/s : sous le minimum de %s (%d kb/s pour un %s)%s", mi.VideoKbps, trk, min, strings.ReplaceAll(key, "/", " "), label)
			}
		}
	}
	if ek == encSD {
		if q.SDMinKbps > 0 && mi.VideoKbps > 0 && mi.VideoKbps < q.SDMinKbps {
			block("débit vidéo de %d kb/s : sous le minimum de %s pour un rip SD (%d kb/s)", mi.VideoKbps, trk, q.SDMinKbps)
		}
		var audioBytes int64
		for _, a := range mi.Audios {
			audioBytes += a.Bytes
			if isAC3(a) && q.SDMaxAC3 > 0 && a.Kbps > q.SDMaxAC3 {
				block("AC3 à %d kb/s : %s limite l'AC3 des rips SD à %d kb/s", a.Kbps, trk, q.SDMaxAC3)
			}
		}
		if audioBytes > 0 && mi.VideoBytes > 0 && audioBytes > mi.VideoBytes {
			block("l'audio pèse plus que la vidéo : refusé sur %s pour un rip SD", trk)
		}
		if mi.Width%16 != 0 || mi.Height%16 != 0 {
			warn("résolution %d×%d : %s demande un multiple de 16 pour les rips SD", mi.Width, mi.Height, trk)
		}
	}

	// Profondeur de couleur.
	if strings.HasPrefix(vf, "av1") && strings.HasPrefix(res, "2160") && mi.BitDepth > 0 && mi.BitDepth < 10 {
		block("AV1 4K en %d bits : %s exige le 10 bits", mi.BitDepth, trk)
	}
	if (ek == encBluRay || ek == encWEB) && mi.Library != "" && mi.BitDepth >= 10 && mi.HDR == "" && rel.HDR == "" {
		low := strings.ToLower(rel.Name + " " + in.Title)
		if !strings.Contains(low, "10bit") && !strings.Contains(low, "hi10") {
			warn("encode %s en 10 bits : %s demande de l'indiquer dans le titre et la description (et un vrai gain visuel)", mi.Library, trk)
		}
	}

	// Audio.
	for i, a := range mi.Audios {
		n := fmt.Sprintf("piste audio %d (%s)", i+1, firstNonEmpty(a.Name, a.Format))
		switch {
		case isMP3(a):
			if ek != encSD {
				block("%s en MP3 : %s n'accepte le MP3 que dans les rips SD", n, trk)
			}
		case strings.HasPrefix(strings.ToLower(a.Format), "flac") && a.Channels > 2:
			block("%s en FLAC %d canaux : %s n'accepte le FLAC qu'en mono ou stéréo", n, a.Channels, trk)
		case isPCM(a):
			if kind != TypeRemux && kind != TypeFullDisc {
				warn("%s en PCM : hors de la liste de %s pour un encode, vérifie", n, trk)
			}
		case !audioListed(a) && a.Format != "":
			warn("%s : codec absent de la liste de %s", n, trk)
		}
		if ek == encBluRay {
			if isAAC(a) && !in.Animation {
				warn("%s en AAC : interdit pour les encodes Blu-ray sur %s (toléré pour l'animation)", n, trk)
			}
			if q.HDMinAudioKbps > 0 && a.Kbps > 0 && a.Kbps < q.HDMinAudioKbps {
				warn("%s à %d kb/s : %s demande %d kb/s au moins (sauf source plus faible)", n, a.Kbps, trk, q.HDMinAudioKbps)
			}
		}
	}

	// Sous-titres image dans un encode.
	if ek == encBluRay || ek == encWEB {
		for _, s := range mi.Subs {
			sf := strings.ToLower(s.Format)
			if strings.Contains(sf, "pgs") || strings.Contains(sf, "vobsub") {
				warn("sous-titres %s dans un encode : %s ne les accepte que s'ils sont optionnels (OCR attendu)", s.Format, trk)
				break
			}
		}
	}
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// SourceLine : la source d'un encode, à citer dans la description.
func SourceLine(rel Release, kind string, mi MediaInfo, res string) string {
	switch encodeKind(rel, kind, mi, res) {
	case encBluRay:
		if strings.HasPrefix(res, "2160") {
			return "UHD BluRay"
		}
		return firstNonEmpty(rel.Source, "BluRay")
	case encSD:
		if rel.Source == "DVD" {
			return "DVD"
		}
		return firstNonEmpty(rel.Source, "BluRay")
	case encWEB:
		if rel.Provider != "" {
			return "WEB-DL " + rel.Provider
		}
		return "WEB-DL"
	}
	return ""
}

// WebLine : pour un WEB-DL non retouché, la plateforme, la résolution, le
// codec et le profil (débit), que les trackers demandent dans la description.
func WebLine(rel Release, kind string, mi MediaInfo, res string) string {
	if kind != TypeWEB || encodeKind(rel, kind, mi, res) != encNone || webKind(rel, mi) != "webdl" {
		return ""
	}
	parts := []string{"WEB-DL"}
	if rel.Provider != "" {
		parts[0] += " " + rel.Provider
	}
	if res != "" {
		parts = append(parts, res)
	}
	c := CodecFamily(mi.VideoFmt)
	if c == "" {
		c = CodecFamily(rel.Codec)
	}
	if l := codecLabel(c); l != "" {
		parts = append(parts, l)
	}
	if mi.VideoKbps > 0 {
		parts = append(parts, thousands(mi.VideoKbps)+" kb/s")
	}
	return strings.Join(parts, " · ")
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "\u202f" + s[i:]
	}
	return s
}

// LangCode : « English » → « en » (langue originale donnée par Radarr).
func LangCode(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	for code, names := range langNames {
		for _, x := range names {
			if n == x {
				return code
			}
		}
	}
	return ""
}
