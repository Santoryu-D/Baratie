package fiche

import (
	"regexp"
	"strconv"
	"strings"
)

// MediaInfo : ce qui sert à la fiche, lu dans la sortie texte de MediaInfo
// (celle de mediainfo.js dans la page, en anglais).
type MediaInfo struct {
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	Interlaced bool     `json:"interlaced"`
	VideoFmt   string   `json:"videoFormat"`        // AVC, HEVC…
	Library    string   `json:"library,omitempty"`  // x264, x265 (encode)
	HDR        string   `json:"hdr,omitempty"`      // HDR format
	BitDepth   int      `json:"bitDepth,omitempty"` // 8, 10
	AudioLangs []string `json:"audioLangs"`         // langues des pistes audio
	SubLangs   []string `json:"subLangs"`           // langues des sous-titres
	AudioFmt   string   `json:"audioFormat"`        // piste par défaut (ou première)
	Channels   string   `json:"channels,omitempty"` // 5.1…
	Empty      bool     `json:"empty"`              // rien lu

	// 0.5.0 : de quoi appliquer les règles d'encodage.
	Container   string     `json:"container,omitempty"` // Matroska, MPEG-4, AVI, MPEG-TS
	Duration    float64    `json:"duration,omitempty"`  // secondes
	VideoKbps   int        `json:"videoKbps,omitempty"` // débit de la piste vidéo
	VideoBytes  int64      `json:"videoBytes,omitempty"`
	VideoCodec  string     `json:"videoCodecId,omitempty"` // Codec ID (XVID, DX50, V_MPEGH/ISO/HEVC…)
	LibraryRaw  string     `json:"libraryRaw,omitempty"`   // Writing library complet
	Application string     `json:"application,omitempty"`  // Writing application (General)
	Settings    string     `json:"-"`                      // Encoding settings
	Audios      []Audio    `json:"audios,omitempty"`
	Subs        []Subtitle `json:"subs,omitempty"`
}

// Audio : une piste audio.
type Audio struct {
	Lang     string `json:"lang"`
	Format   string `json:"format"`   // Format (AC-3, E-AC-3, DTS, MLP FBA, AAC, FLAC, MPEG Audio…)
	Name     string `json:"name"`     // Commercial name
	Profile  string `json:"profile"`  // Format profile (Layer 3…)
	Channels int    `json:"channels"` // nombre de canaux
	Kbps     int    `json:"kbps"`     // débit
	Bytes    int64  `json:"bytes"`    // taille du flux
	Default  bool   `json:"default"`
}

// Subtitle : une piste de sous-titres.
type Subtitle struct {
	Lang   string `json:"lang"`
	Format string `json:"format"` // UTF-8, ASS, PGS, VobSub…
	Forced bool   `json:"forced"`
}

var (
	digits   = regexp.MustCompile(`[^0-9]`)
	sectRe   = regexp.MustCompile(`^(General|Video|Audio|Text|Menu|Image)(\s*#\d+)?$`)
	chanNums = map[string]string{"1": "1.0", "2": "2.0", "3": "2.1", "6": "5.1", "7": "6.1", "8": "7.1"}
	numRe    = regexp.MustCompile(`([0-9][0-9 \x{00a0}\x{202f}]*(?:[.,][0-9]+)?)\s*([A-Za-z]*)`)
	durHMS   = regexp.MustCompile(`^(\d+):(\d{2}):(\d{2})(?:\.\d+)?`)
	durPart  = regexp.MustCompile(`(\d+)\s*(h|min|s|ms)\b`)
)

// number : « 8 500 kb/s » → 8500 et « kb ». Les espaces (même insécables)
// séparent les milliers.
func number(v string) (float64, string) {
	m := numRe.FindStringSubmatch(v)
	if m == nil {
		return 0, ""
	}
	s := strings.NewReplacer(" ", "", " ", "", " ", "", ",", ".").Replace(m[1])
	f, _ := strconv.ParseFloat(s, 64)
	return f, strings.ToLower(m[2])
}

// kbps : « 24.8 Mb/s », « 8 500 kb/s », « 448 kb/s » → kb/s.
func kbps(v string) int {
	f, u := number(v)
	switch {
	case strings.HasPrefix(u, "m"):
		f *= 1000
	case strings.HasPrefix(u, "g"):
		f *= 1000 * 1000
	case u == "b" || strings.HasPrefix(u, "bps"):
		f /= 1000
	}
	return int(f + 0.5)
}

// bytesOf : « 4.20 GiB (85%) », « 1 509 MiB », « 350 KiB » → octets.
func bytesOf(v string) int64 {
	f, u := number(v)
	mult := map[string]float64{"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "kb": 1e3, "mb": 1e6, "gb": 1e9, "bytes": 1, "byte": 1, "b": 1}[u]
	if mult == 0 {
		mult = 1
	}
	return int64(f * mult)
}

// seconds : « 1 h 45 min », « 45 min 30 s », « 01:45:12.345 ».
func seconds(v string) float64 {
	if m := durHMS.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		s, _ := strconv.Atoi(m[3])
		return float64(h*3600 + mi*60 + s)
	}
	t := 0.0
	for _, m := range durPart.FindAllStringSubmatch(v, -1) {
		n, _ := strconv.ParseFloat(m[1], 64)
		switch m[2] {
		case "h":
			t += n * 3600
		case "min":
			t += n * 60
		case "s":
			t += n
		case "ms":
			t += n / 1000
		}
	}
	return t
}

// ParseMediaInfo lit la sortie texte.
func ParseMediaInfo(text string) MediaInfo {
	var mi MediaInfo
	if strings.TrimSpace(text) == "" {
		mi.Empty = true
		return mi
	}
	section := ""
	var cur *Audio
	var sub *Subtitle
	videoSeen := false
	videoDur, generalDur := 0.0, 0.0
	nominal := 0
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if sectRe.MatchString(line) {
			section = strings.Fields(line)[0]
			cur, sub = nil, nil
			if section == "Audio" {
				mi.Audios = append(mi.Audios, Audio{})
				cur = &mi.Audios[len(mi.Audios)-1]
			}
			if section == "Text" {
				mi.Subs = append(mi.Subs, Subtitle{})
				sub = &mi.Subs[len(mi.Subs)-1]
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch section {
		case "General":
			switch k {
			case "Format":
				if mi.Container == "" {
					mi.Container = v
				}
			case "Duration":
				if generalDur == 0 {
					generalDur = seconds(v)
				}
			case "Writing application":
				mi.Application = v
			}
		case "Video":
			if videoSeen && k == "ID" {
				section = "Video2" // seconde vidéo (DV) : ignorée
				continue
			}
			videoSeen = true
			switch k {
			case "Width":
				mi.Width, _ = strconv.Atoi(digits.ReplaceAllString(v, ""))
			case "Height":
				mi.Height, _ = strconv.Atoi(digits.ReplaceAllString(v, ""))
			case "Scan type":
				mi.Interlaced = strings.Contains(strings.ToLower(v), "interlaced") || strings.Contains(strings.ToLower(v), "mbaff")
			case "Format":
				if mi.VideoFmt == "" {
					mi.VideoFmt = v
				}
			case "Codec ID":
				if mi.VideoCodec == "" {
					mi.VideoCodec = v
				}
			case "Writing library":
				mi.LibraryRaw = v
				lv := strings.ToLower(v)
				if strings.HasPrefix(lv, "x264") {
					mi.Library = "x264"
				} else if strings.HasPrefix(lv, "x265") {
					mi.Library = "x265"
				}
			case "Encoding settings":
				mi.Settings = v
			case "HDR format":
				mi.HDR = v
			case "Bit depth":
				mi.BitDepth, _ = strconv.Atoi(digits.ReplaceAllString(v, ""))
			case "Bit rate":
				mi.VideoKbps = kbps(v)
			case "Nominal bit rate":
				nominal = kbps(v)
			case "Stream size":
				mi.VideoBytes = bytesOf(v)
			case "Duration":
				if videoDur == 0 {
					videoDur = seconds(v)
				}
			}
		case "Audio":
			if cur == nil {
				continue
			}
			switch k {
			case "Language":
				cur.Lang = v
			case "Commercial name":
				cur.Name = v
			case "Format":
				if cur.Format == "" {
					cur.Format = v
				}
			case "Format profile":
				cur.Profile = v
			case "Channel(s)":
				cur.Channels, _ = strconv.Atoi(digits.ReplaceAllString(v, ""))
			case "Bit rate":
				cur.Kbps = kbps(v)
			case "Stream size":
				cur.Bytes = bytesOf(v)
			case "Default":
				cur.Default = strings.EqualFold(v, "Yes")
			}
		case "Text":
			if sub == nil {
				continue
			}
			switch k {
			case "Language":
				sub.Lang = v
			case "Format":
				if sub.Format == "" {
					sub.Format = v
				}
			case "Forced":
				sub.Forced = strings.EqualFold(v, "Yes")
			}
		}
	}
	mi.Duration = videoDur
	if mi.Duration == 0 {
		mi.Duration = generalDur
	}
	if mi.VideoKbps == 0 {
		mi.VideoKbps = nominal
	}
	if mi.VideoKbps == 0 && mi.VideoBytes > 0 && mi.Duration > 0 {
		mi.VideoKbps = int(float64(mi.VideoBytes)*8/1000/mi.Duration + 0.5)
	}
	for _, a := range mi.Audios {
		mi.AudioLangs = append(mi.AudioLangs, a.Lang)
	}
	for _, s := range mi.Subs {
		mi.SubLangs = append(mi.SubLangs, s.Lang)
	}
	if len(mi.Audios) > 0 {
		main := mi.Audios[0]
		for _, a := range mi.Audios {
			if a.Default {
				main = a
				break
			}
		}
		mi.AudioFmt = main.Name
		if mi.AudioFmt == "" {
			mi.AudioFmt = main.Format
		}
		mi.Channels = chanNums[strconv.Itoa(main.Channels)]
	}
	mi.Empty = mi.Width == 0 && len(mi.Audios) == 0
	return mi
}

func isFrench(lang string) bool {
	l := strings.ToLower(lang)
	return strings.HasPrefix(l, "french") || l == "fr" || strings.HasPrefix(l, "fr-") || strings.Contains(l, "français") || strings.Contains(l, "francais")
}

// HasFrenchAudio / HasFrenchSubs : au moins une piste en français.
func (mi MediaInfo) HasFrenchAudio() bool { return anyFrench(mi.AudioLangs) }
func (mi MediaInfo) HasFrenchSubs() bool  { return anyFrench(mi.SubLangs) }

func anyFrench(l []string) bool {
	for _, x := range l {
		if isFrench(x) {
			return true
		}
	}
	return false
}

// Resolution : la résolution d'après l'image (largeur d'abord : un 1080p au
// format 2.40 fait 1920×800).
func (mi MediaInfo) Resolution() string {
	if mi.Width == 0 && mi.Height == 0 {
		return ""
	}
	sfx := "p"
	if mi.Interlaced {
		sfx = "i"
	}
	switch {
	case mi.Width >= 7000 || mi.Height >= 3500:
		return "4320p"
	case mi.Width >= 3400 || mi.Height >= 1800:
		return "2160p"
	case mi.Width >= 1700 || mi.Height >= 900:
		return "1080" + sfx
	case mi.Width >= 1200 || mi.Height >= 650:
		return "720p"
	case mi.Height > 490 || mi.Width > 720:
		return "576" + sfx
	case mi.Height > 0:
		return "480" + sfx
	}
	return ""
}
