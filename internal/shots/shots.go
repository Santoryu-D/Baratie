// Package shots : captures d'écran d'un film pour sa fiche (règles courantes des trackers :
// PNG, proportions d'affichage réelles (DAR), sans sous-titres, au moins 3).
//
// ffmpeg ne fait que LIRE le film ; les images vont dans un dossier de
// travail (configuration de Baratie), jamais à côté du film.
package shots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxBytes : au-delà, la capture est recompressée (une image 1920×1080 en
// 8 bits pèse 6 Mo au plus ; imgbb accepte 32 Mo, PixHost 10).
const MaxBytes = 10 << 20

// Shot : une capture.
type Shot struct {
	Index int     `json:"index"`
	At    float64 `json:"at"` // secondes
	Path  string  `json:"-"`
	Size  int64   `json:"size"`
	Error string  `json:"error,omitempty"`
}

// Tool : où trouver ffmpeg et ffprobe.
type Tool struct {
	FFmpeg  string
	FFprobe string
}

// Find cherche ffmpeg et ffprobe (BARATIE_FFMPEG peut donner ffmpeg).
func Find() (Tool, error) {
	ff := os.Getenv("BARATIE_FFMPEG")
	if ff == "" {
		ff, _ = exec.LookPath("ffmpeg")
	}
	fp, _ := exec.LookPath("ffprobe")
	if fp == "" && ff != "" {
		if p := filepath.Join(filepath.Dir(ff), "ffprobe"); fileExists(p) {
			fp = p
		}
	}
	if ff == "" || fp == "" {
		return Tool{}, errors.New("ffmpeg absent : les captures d'écran demandent l'image Docker 0.4 ou plus")
	}
	return Tool{FFmpeg: ff, FFprobe: fp}, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Duration : la durée du film, en secondes.
// Celle de l'image d'abord : une piste de sous-titres peut durer plus
// longtemps que la vidéo.
func (t Tool) Duration(ctx context.Context, file string) (float64, error) {
	read := func(args ...string) float64 {
		out, err := exec.CommandContext(ctx, t.FFprobe, append([]string{"-v", "error"}, append(args, "-of", "default=noprint_wrappers=1:nokey=1", file)...)...).Output()
		if err != nil {
			return 0
		}
		d, _ := strconv.ParseFloat(strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), 64)
		return d
	}
	if d := read("-select_streams", "v:0", "-show_entries", "stream=duration"); d > 0 {
		return d, nil
	}
	if d := read("-show_entries", "format=duration"); d > 0 {
		return d, nil
	}
	return 0, errors.New("durée illisible (ffprobe)")
}

// Positions : n moments répartis de 15 % à 75 % du film (loin des génériques).
func Positions(dur float64, n int) []float64 {
	if n < 1 {
		n = 1
	}
	out := make([]float64, n)
	for i := range out {
		f := 0.15
		if n > 1 {
			f = 0.15 + 0.60*float64(i)/float64(n-1)
		}
		out[i] = dur * f
	}
	return out
}

// Other : un autre moment pour refaire une capture (entre 10 % et 85 %).
func Other(dur float64) float64 {
	return dur * (0.10 + 0.75*rand.Float64())
}

// Capture : une image PNG au moment « at », image principale seulement (aucun
// sous-titre, aucune autre piste), aux proportions d'affichage réelles,
// 1920 px de large au plus, HDR converti en SDR quand ffmpeg le permet.
func (t Tool) Capture(ctx context.Context, s *Set, at float64, out string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return 0, err
	}
	run := func(extra ...string) error {
		args := []string{"-nostdin", "-v", "error", "-y",
			"-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", s.File,
			"-map", "0:v:0", "-frames:v", "1", "-an", "-sn", "-dn",
			"-vf", s.Video.Filters(MaxWidth, s.Tonemap),
			"-f", "image2", "-c:v", "png"}
		args = append(args, extra...)
		args = append(args, out)
		var errb bytes.Buffer
		cmd := exec.CommandContext(ctx, t.FFmpeg, args...)
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(errb.String())
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return fmt.Errorf("capture impossible : %s", msg)
		}
		return nil
	}
	if err := run(); err != nil {
		return 0, err
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		// Rien à ce moment (fin de flux, durée annoncée trop longue) : un peu
		// plus tôt.
		at *= 0.7
		if err := run(); err != nil {
			return 0, err
		}
		if st, err = os.Stat(out); err != nil || st.Size() == 0 {
			return 0, errors.New("capture vide")
		}
	}
	// Trop lourde pour l'hébergeur (rare : une image 1920×1080 en 8 bits
	// pèse 6 Mo au plus) : recompressée sans perte.
	if st.Size() > MaxBytes {
		if err := run("-pred", "mixed", "-compression_level", "100"); err == nil {
			st, _ = os.Stat(out)
		}
	}
	if st.Size() > MaxBytes {
		return st.Size(), fmt.Errorf("capture de %.1f Mo : trop lourde (plus de 10 Mo), même recompressée sans perte", float64(st.Size())/(1<<20))
	}
	return st.Size(), nil
}

// Set : les captures d'un film, dans leur dossier de travail.
type Set struct {
	Dir     string   `json:"-"`
	File    string   `json:"-"`
	Dur     float64  `json:"duration"`
	Video   Video    `json:"-"`
	Tonemap bool     `json:"-"` // HDR converti en SDR
	Notes   []string `json:"notes,omitempty"`
	Warn    string   `json:"warn,omitempty"`
	Shots   []Shot   `json:"shots"`
}

// ErrDolbyVision : Dolby Vision sans image de base lisible (profil 5).
var ErrDolbyVision = errors.New("Dolby Vision seul (profil 5, sans base HDR10) : ffmpeg ne sait pas en tirer des couleurs justes, les captures sortiraient vertes et violettes. Prends-les depuis une autre version du film, ou publie sans captures (la description garde le nom de la release)")

// Make prend n captures du film dans dir.
func (t Tool) Make(ctx context.Context, file, dir string, n int) (*Set, error) {
	v, err := t.Probe(ctx, file)
	if err != nil {
		return nil, err
	}
	if v.Range == DVOnly {
		return nil, ErrDolbyVision
	}
	dur, err := t.Duration(ctx, file)
	if err != nil {
		return nil, err
	}
	s := &Set{Dir: dir, File: file, Dur: dur, Video: v}
	if w, h := v.Target(MaxWidth); w != v.DisplayWidth() {
		s.Notes = append(s.Notes, fmt.Sprintf("source %d×%d réduite en %d×%d (une capture 4K pèse plus de 30 Mo)", v.DisplayWidth(), v.Height, w, h))
	}
	if v.Range == HDR10 || v.Range == HLG {
		name := v.Range
		if v.DVProfile != 0 {
			name = "Dolby Vision (base " + v.Range + ")"
		}
		if t.HasZscale(ctx) {
			s.Tonemap = true
			s.Notes = append(s.Notes, name+" converti en SDR pour des couleurs naturelles")
		} else {
			s.Warn = name + " : ce ffmpeg ne sait pas convertir en SDR (filtre zscale absent), les captures seront délavées"
		}
	}
	for i, at := range Positions(dur, n) {
		sh := Shot{Index: i, At: at, Path: filepath.Join(dir, fmt.Sprintf("capture-%d-%d.png", i+1, time.Now().UnixNano()))}
		sz, err := t.Capture(ctx, s, at, sh.Path)
		sh.Size = sz
		if err != nil {
			sh.Error = err.Error()
		}
		s.Shots = append(s.Shots, sh)
	}
	return s, nil
}

// Redo refait la capture i à un autre moment (l'ancienne image est retirée :
// c'est un fichier de travail de Baratie, jamais un fichier de ta médiathèque).
func (t Tool) Redo(ctx context.Context, s *Set, i int) error {
	if i < 0 || i >= len(s.Shots) {
		return errors.New("capture inconnue")
	}
	old := s.Shots[i].Path
	sh := Shot{Index: i, At: Other(s.Dur), Path: filepath.Join(s.Dir, fmt.Sprintf("capture-%d-%d.png", i+1, time.Now().UnixNano()))}
	sz, err := t.Capture(ctx, s, sh.At, sh.Path)
	sh.Size = sz
	if err != nil {
		sh.Error = err.Error()
	}
	s.Shots[i] = sh
	if inside(old, s.Dir) {
		_ = os.Remove(old)
	}
	return nil
}

// Clean retire le dossier de travail (et lui seul).
func (s *Set) Clean() {
	if s != nil && s.Dir != "" && strings.Contains(filepath.Base(s.Dir), "captures-") {
		_ = os.RemoveAll(s.Dir)
	}
}

func inside(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// BBCode : les vignettes en rangées de deux, chacune liée à l'image en taille
// réelle.
func BBCode(hosted []Hosted) string {
	var b strings.Builder
	b.WriteString("[center]")
	for i, h := range hosted {
		if i > 0 && i%2 == 0 {
			b.WriteString("\n")
		} else if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "[url=%s][img]%s[/img][/url]", h.Show, h.Thumb)
	}
	b.WriteString("[/center]")
	return b.String()
}

// Hosted : une capture hébergée.
type Hosted struct {
	Show  string `json:"show"`
	Thumb string `json:"thumb"`
}
