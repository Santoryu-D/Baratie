package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"testing"
)

// Dessine l'icône de D3X (le même dessin que ui/favicon.svg, sur une grille de
// 64) avec un lissage par sur-échantillonnage, puis écrit icon.ico (16, 24,
// 32, 48, 64, 256 px) et d3x-256.png (icône du .exe).
//
//	D3X_GENICON=1 go test ./internal/desktop -run TestGenIcon
func TestGenIcon(t *testing.T) {
	if os.Getenv("D3X_GENICON") == "" {
		t.Skip("D3X_GENICON non défini")
	}
	var pngs [][]byte
	var sizes []int
	for _, n := range []int{16, 24, 32, 48, 64, 256} {
		var b bytes.Buffer
		if err := png.Encode(&b, render(n)); err != nil {
			t.Fatal(err)
		}
		pngs = append(pngs, b.Bytes())
		sizes = append(sizes, n)
		if n == 256 {
			if err := os.WriteFile("d3x-256.png", b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// ICO : en-tête, une entrée par taille, puis les PNG (accepté depuis Vista).
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, uint16(len(pngs))})
	offset := 6 + 16*len(pngs)
	for i, p := range pngs {
		w := byte(sizes[i])
		if sizes[i] == 256 {
			w = 0
		}
		ico.Write([]byte{w, w, 0, 0})
		binary.Write(&ico, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&ico, binary.LittleEndian, [2]uint32{uint32(len(p)), uint32(offset)})
		offset += len(p)
	}
	for _, p := range pngs {
		ico.Write(p)
	}
	if err := os.WriteFile("icon.ico", ico.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

type pt struct{ x, y float64 }

func segDist(p, a, b pt) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	l := dx*dx + dy*dy
	u := ((p.x-a.x)*dx + (p.y-a.y)*dy) / l
	u = math.Max(0, math.Min(1, u))
	cx, cy := a.x+u*dx-p.x, a.y+u*dy-p.y
	return math.Hypot(cx, cy)
}

var (
	ground = color.NRGBA{0x1b, 0x23, 0x40, 0xff}
	blue   = color.NRGBA{0x7d, 0x9b, 0xff, 0xff}
	white  = color.NRGBA{0xe8, 0xec, 0xf8, 0xff}
)

// sample : la couleur au point p de la grille de 64 (ou transparent).
func sample(p pt) (color.NRGBA, bool) {
	// Carré aux coins arrondis (rayon 14).
	const r = 14.0
	qx := math.Max(math.Abs(p.x-32)-(32-r), 0)
	qy := math.Max(math.Abs(p.y-32)-(32-r), 0)
	if math.Hypot(qx, qy) > r {
		return color.NRGBA{}, false
	}
	// X : deux traits de 6, bouts arrondis.
	if segDist(p, pt{34, 22}, pt{50, 42}) <= 3 || segDist(p, pt{50, 22}, pt{34, 42}) <= 3 {
		return white, true
	}
	// D : contour de 6 (haut, demi-cercle, bas, côté gauche).
	d := math.Min(segDist(p, pt{14, 18}, pt{25, 18}), segDist(p, pt{25, 46}, pt{14, 46}))
	d = math.Min(d, segDist(p, pt{14, 46}, pt{14, 18}))
	if p.x >= 25 {
		d = math.Min(d, math.Abs(math.Hypot(p.x-25, p.y-32)-14))
	}
	if d <= 3 {
		return blue, true
	}
	return ground, true
}

func render(n int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	const ss = 6 // sur-échantillonnage par côté
	scale := 64.0 / float64(n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					p := pt{(float64(x) + (float64(sx)+0.5)/ss) * scale, (float64(y) + (float64(sy)+0.5)/ss) * scale}
					if c, ok := sample(p); ok {
						r += float64(c.R)
						g += float64(c.G)
						b += float64(c.B)
						a++
					}
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / (ss * ss))})
		}
	}
	return img
}
