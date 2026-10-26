// Package probe lit un film avec ffprobe (lecture seule, en-têtes seulement)
// et le traduit dans le format MediaInfo de la fiche, pour que le Garde-manger
// applique les règles du tracker sans ouvrir la fiche (Baratie 0.6.0).
//
// ffprobe ne donne pas tout ce que donne MediaInfo : ni la « Writing library »
// d'un x264, ni ses réglages. Les règles qui en dépendent (CRF, encodeur) ne
// sont jugées qu'à la fiche.
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"baratie/internal/fiche"
)

type stream struct {
	Type     string            `json:"codec_type"`
	Codec    string            `json:"codec_name"`
	Profile  string            `json:"profile"`
	Tag      string            `json:"codec_tag_string"`
	Width    int               `json:"width"`
	Height   int               `json:"height"`
	PixFmt   string            `json:"pix_fmt"`
	BitsRaw  string            `json:"bits_per_raw_sample"`
	Transfer string            `json:"color_transfer"`
	FieldOrd string            `json:"field_order"`
	Channels int               `json:"channels"`
	BitRate  string            `json:"bit_rate"`
	Tags     map[string]string `json:"tags"`
	Dispo    map[string]int    `json:"disposition"`
	SideData []map[string]any  `json:"side_data_list"`
	Duration string            `json:"duration"`
}

type output struct {
	Streams []stream `json:"streams"`
	Format  struct {
		Name     string            `json:"format_name"`
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

// File lit un film.
func File(ctx context.Context, ffprobe, path string) (fiche.MediaInfo, error) {
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			msg := strings.TrimSpace(string(ee.Stderr))
			if len(msg) > 150 {
				msg = msg[:150]
			}
			return fiche.MediaInfo{}, fmt.Errorf("ffprobe : %s", msg)
		}
		return fiche.MediaInfo{}, fmt.Errorf("ffprobe : %v", err)
	}
	return Parse(out)
}

// tag : une étiquette, sous son nom simple ou avec la langue (« BPS-eng »).
func tag(t map[string]string, k string) string {
	for key, v := range t {
		kk := strings.ToUpper(key)
		if kk == k || strings.HasPrefix(kk, k+"-") {
			return v
		}
	}
	return ""
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func atoi64(s string) int64 { n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64); return n }

func secs(s string) float64 { f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return f }

// Langues ISO 639-2 (Matroska) → noms anglais (MediaInfo).
var lang3 = map[string]string{
	"fre": "French", "fra": "French", "eng": "English", "jpn": "Japanese", "ita": "Italian", "spa": "Spanish",
	"ger": "German", "deu": "German", "kor": "Korean", "chi": "Chinese", "zho": "Chinese", "cmn": "Mandarin",
	"yue": "Cantonese", "rus": "Russian", "por": "Portuguese", "swe": "Swedish", "dan": "Danish", "nor": "Norwegian",
	"nob": "Norwegian", "dut": "Dutch", "nld": "Dutch", "pol": "Polish", "tur": "Turkish", "tha": "Thai",
	"fin": "Finnish", "gre": "Greek", "ell": "Greek", "heb": "Hebrew", "ara": "Arabic", "cze": "Czech",
	"ces": "Czech", "hun": "Hungarian", "rum": "Romanian", "ron": "Romanian", "per": "Persian", "fas": "Persian",
	"ind": "Indonesian", "hin": "Hindi", "tam": "Tamil", "tel": "Telugu", "ice": "Icelandic", "isl": "Icelandic",
	"ukr": "Ukrainian", "srp": "Serbian", "hrv": "Croatian", "ben": "Bengali", "mal": "Malayalam", "tgl": "Tagalog",
	"fil": "Filipino", "vie": "Vietnamese",
}

func langName(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if l == "" || l == "und" || l == "zxx" || l == "mul" {
		return ""
	}
	if n := lang3[l]; n != "" {
		return n
	}
	return l
}

var videoFmt = map[string]string{"h264": "AVC", "hevc": "HEVC", "av1": "AV1", "mpeg4": "MPEG-4 Visual", "msmpeg4v3": "MPEG-4 Visual",
	"mpeg2video": "MPEG Video", "mpeg1video": "MPEG Video", "vc1": "VC-1", "wmv3": "VC-1", "vp9": "VP9", "jpeg2000": "JPEG 2000"}

var audioFmt = map[string]string{"ac3": "AC-3", "eac3": "E-AC-3", "dts": "DTS", "truehd": "MLP FBA", "aac": "AAC", "flac": "FLAC",
	"mp3": "MPEG Audio", "mp2": "MPEG Audio", "opus": "Opus", "vorbis": "Vorbis"}

var subFmt = map[string]string{"subrip": "UTF-8", "ass": "ASS", "ssa": "SSA", "hdmv_pgs_subtitle": "PGS", "dvd_subtitle": "VobSub", "mov_text": "Timed Text", "webvtt": "WebVTT"}

// Parse traduit la sortie JSON de ffprobe.
func Parse(b []byte) (fiche.MediaInfo, error) {
	var o output
	if err := json.Unmarshal(b, &o); err != nil {
		return fiche.MediaInfo{}, errors.New("ffprobe : réponse illisible")
	}
	var mi fiche.MediaInfo
	switch fn := o.Format.Name; {
	case strings.Contains(fn, "matroska"):
		mi.Container = "Matroska"
	case strings.Contains(fn, "mp4") || strings.Contains(fn, "mov"):
		mi.Container = "MPEG-4"
	case fn == "avi":
		mi.Container = "AVI"
	case fn == "mpegts":
		mi.Container = "MPEG-TS"
	default:
		mi.Container = fn
	}
	mi.Duration = secs(o.Format.Duration)
	mi.Application = tag(o.Format.Tags, "ENCODER")
	audioKbps := 0
	videoSeen := false
	for _, s := range o.Streams {
		switch s.Type {
		case "video":
			if videoSeen || s.Dispo["attached_pic"] == 1 {
				continue
			}
			videoSeen = true
			mi.Width, mi.Height = s.Width, s.Height
			mi.Interlaced = s.FieldOrd != "" && s.FieldOrd != "progressive" && s.FieldOrd != "unknown"
			mi.VideoFmt = videoFmt[s.Codec]
			if mi.VideoFmt == "" {
				mi.VideoFmt = s.Codec
			}
			mi.VideoCodec = strings.ToUpper(s.Tag)
			mi.BitDepth = atoi(s.BitsRaw)
			if mi.BitDepth == 0 {
				mi.BitDepth = 8
				if strings.Contains(s.PixFmt, "10") {
					mi.BitDepth = 10
				} else if strings.Contains(s.PixFmt, "12") {
					mi.BitDepth = 12
				}
			}
			if s.Transfer == "smpte2084" {
				mi.HDR = "SMPTE ST 2084"
			} else if s.Transfer == "arib-std-b67" {
				mi.HDR = "HLG"
			}
			for _, sd := range s.SideData {
				if t, _ := sd["side_data_type"].(string); strings.Contains(strings.ToLower(t), "dovi") {
					mi.HDR = "Dolby Vision" + map[bool]string{true: ", " + mi.HDR, false: ""}[mi.HDR != ""]
				}
			}
			mi.VideoKbps = atoi(s.BitRate) / 1000
			if mi.VideoKbps == 0 {
				mi.VideoKbps = atoi(tag(s.Tags, "BPS")) / 1000
			}
			mi.VideoBytes = atoi64(tag(s.Tags, "NUMBER_OF_BYTES"))
			if mi.Library == "" {
				if e := strings.ToLower(tag(s.Tags, "ENCODER")); strings.HasPrefix(e, "x264") || strings.HasPrefix(e, "x265") {
					mi.Library, mi.LibraryRaw = e[:4], tag(s.Tags, "ENCODER")
				}
			}
		case "audio":
			a := fiche.Audio{Lang: langName(tag(s.Tags, "LANGUAGE")), Format: audioFmt[s.Codec], Channels: s.Channels, Default: s.Dispo["default"] == 1}
			switch {
			case a.Format == "" && strings.HasPrefix(s.Codec, "pcm"):
				a.Format = "PCM"
			case a.Format == "":
				a.Format = s.Codec
			}
			if s.Codec == "mp3" {
				a.Profile = "Layer 3"
			}
			if s.Codec == "dts" && s.Profile != "" {
				a.Name = s.Profile // DTS-HD MA, DTS:X…
			}
			if s.Codec == "eac3" && strings.Contains(s.Profile, "Atmos") {
				a.Name = "Dolby Digital Plus with Dolby Atmos"
			}
			a.Kbps = atoi(s.BitRate) / 1000
			if a.Kbps == 0 {
				a.Kbps = atoi(tag(s.Tags, "BPS")) / 1000
			}
			a.Bytes = atoi64(tag(s.Tags, "NUMBER_OF_BYTES"))
			audioKbps += a.Kbps
			mi.Audios = append(mi.Audios, a)
		case "subtitle":
			f := subFmt[s.Codec]
			if f == "" {
				f = s.Codec
			}
			mi.Subs = append(mi.Subs, fiche.Subtitle{Lang: langName(tag(s.Tags, "LANGUAGE")), Format: f, Forced: s.Dispo["forced"] == 1})
		}
	}
	// Débit vidéo absent (MKV sans statistiques) : débit global moins l'audio.
	if mi.VideoKbps == 0 && mi.VideoBytes > 0 && mi.Duration > 0 {
		mi.VideoKbps = int(float64(mi.VideoBytes)*8/1000/mi.Duration + 0.5)
	}
	if mi.VideoKbps == 0 {
		if total := atoi(o.Format.BitRate) / 1000; total > audioKbps && audioKbps > 0 {
			mi.VideoKbps = total - audioKbps
		}
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
		mi.AudioFmt = main.Format
	}
	mi.Empty = mi.Width == 0 && len(mi.Audios) == 0
	return mi, nil
}
