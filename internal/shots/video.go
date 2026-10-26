package shots

// Baratie 0.4.2 : ce que ffprobe dit de l'image (taille, pixels, HDR, Dolby
// Vision) et la chaîne de filtres qui en découle pour la capture.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MaxWidth : largeur maximale d'une capture. Une image 4K en PNG pèse
// 30 Mo et plus, bien trop lourd pour un hébergeur d'images ; 1920 px reste net.
const MaxWidth = 1920

// Plages dynamiques.
const (
	SDR    = "SDR"
	HDR10  = "HDR10"
	HLG    = "HLG"
	DVOnly = "DV" // Dolby Vision sans image de base exploitable (profil 5)
)

// Video : l'image principale du film.
type Video struct {
	Width, Height  int
	SARNum, SARDen int
	Transfer       string // color_transfer (smpte2084, arib-std-b67…)
	Primaries      string
	Matrix         string
	DVProfile      int // 0 : pas de Dolby Vision
	DVCompat       int // dv_bl_signal_compatibility_id
	Range          string
}

// DisplayWidth : largeur à l'affichage (pixels non carrés remis à l'échelle).
func (v Video) DisplayWidth() int {
	if v.SARNum > 0 && v.SARDen > 0 {
		return even(v.Width * v.SARNum / v.SARDen)
	}
	return v.Width
}

// Target : la taille de la capture (proportions réelles, 1920 px au plus).
func (v Video) Target(max int) (int, int) {
	w, h := v.DisplayWidth(), v.Height
	if w > max && w > 0 {
		h = even(h * max / w)
		w = max
	}
	return w, h
}

func even(n int) int { return n - n%2 }

// Probe lit l'image principale avec ffprobe.
func (t Tool) Probe(ctx context.Context, file string) (Video, error) {
	out, err := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,sample_aspect_ratio,color_transfer,color_primaries,color_space:stream_side_data",
		"-of", "json", file).Output()
	if err != nil {
		return Video{}, fmt.Errorf("ffprobe : %v", err)
	}
	return parseProbe(out)
}

func parseProbe(b []byte) (Video, error) {
	var r struct {
		Streams []struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			SAR       string `json:"sample_aspect_ratio"`
			Transfer  string `json:"color_transfer"`
			Primaries string `json:"color_primaries"`
			Space     string `json:"color_space"`
			Side      []struct {
				Type    string `json:"side_data_type"`
				Profile int    `json:"dv_profile"`
				Compat  int    `json:"dv_bl_signal_compatibility_id"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Video{}, err
	}
	if len(r.Streams) == 0 || r.Streams[0].Width == 0 {
		return Video{}, errors.New("aucune image dans ce fichier")
	}
	s := r.Streams[0]
	v := Video{Width: s.Width, Height: s.Height, Transfer: s.Transfer, Primaries: s.Primaries, Matrix: s.Space, SARNum: 1, SARDen: 1}
	if a, b, ok := strings.Cut(s.SAR, ":"); ok {
		n, _ := strconv.Atoi(a)
		d, _ := strconv.Atoi(b)
		if n > 0 && d > 0 {
			v.SARNum, v.SARDen = n, d
		}
	}
	for _, sd := range s.Side {
		if strings.Contains(strings.ToLower(sd.Type), "dovi") || strings.Contains(strings.ToLower(sd.Type), "dolby vision") {
			v.DVProfile, v.DVCompat = sd.Profile, sd.Compat
			if v.DVProfile == 0 {
				v.DVProfile = -1 // présent, profil non lu
			}
		}
	}
	v.Range = rangeOf(v)
	return v, nil
}

// rangeOf : la plage dynamique de l'image que ffmpeg décode.
// Dolby Vision 7 et 8.1 portent une image HDR10, 8.4 une image HLG, 8.2 une
// image SDR : elles se convertissent comme telles. Le profil 5 (compatibilité
// 0) n'a pas d'image de base lisible sans le traitement Dolby : couleurs
// vertes et violettes garanties.
func rangeOf(v Video) string {
	switch v.Transfer {
	case "smpte2084":
		return HDR10
	case "arib-std-b67":
		return HLG
	}
	if v.DVProfile != 0 {
		switch v.DVCompat {
		case 1, 6:
			return HDR10
		case 4:
			return HLG
		case 2:
			return SDR
		}
		return DVOnly
	}
	return SDR
}

// Filters : la chaîne de filtres de la capture.
// tonemap : convertir le HDR en SDR (demande zscale dans ffmpeg).
func (v Video) Filters(width int, tonemap bool) string {
	w, h := v.Target(width)
	f := []string{fmt.Sprintf("scale=%d:%d:flags=lanczos", w, h), "setsar=1"}
	if tonemap && (v.Range == HDR10 || v.Range == HLG) {
		tin := "smpte2084"
		if v.Range == HLG {
			tin = "arib-std-b67"
		}
		f = append(f,
			"zscale=tin="+tin+":min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100",
			"format=gbrpf32le",
			"zscale=p=bt709",
			"tonemap=tonemap=hable:desat=0",
			"zscale=t=bt709:m=bt709:r=pc")
	}
	// PNG 8 bits : une source 10 bits donnerait un PNG 16 bits, deux fois
	// plus lourd, sans rien de plus à l'écran.
	f = append(f, "format=rgb24")
	return strings.Join(f, ",")
}

// HasZscale : ffmpeg sait-il convertir le HDR (filtre zscale, bibliothèque
// zimg) ?
func (t Tool) HasZscale(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, t.FFmpeg, "-hide_banner", "-filters").Output()
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[1] == "zscale" {
			return true
		}
	}
	return false
}
