package audio

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ---------- MP3 : ID3v2 puis premier en-tête MPEG ----------

func syncsafe(b []byte) int {
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

func readMP3(t *Track, r io.ReaderAt, n int64) error {
	audioStart := int64(0)
	h, err := readAt(r, 0, 10)
	if err != nil {
		return err
	}
	if string(h[:3]) == "ID3" {
		size := int64(syncsafe(h[6:10]))
		audioStart = 10 + size
		if h[5]&0x10 != 0 { // pied de tag
			audioStart += 10
		}
		if size > 0 && size <= 16<<20 {
			if b, err := readAt(r, 10, int(size)); err == nil {
				id3Frames(t, b, int(h[3]), h[5])
			}
		}
	}
	return mpegFrame(t, r, n, audioStart)
}

// id3Frames lit les cadres d'un tag ID3v2.2, 2.3 ou 2.4.
func id3Frames(t *Track, b []byte, ver int, flags byte) {
	if flags&0x80 != 0 && ver < 4 {
		b = unsync(b)
	}
	p := 0
	if flags&0x40 != 0 && len(b) >= 4 { // en-tête étendu
		ext := int(be32(b))
		if ver == 4 {
			ext = syncsafe(b)
		} else {
			ext += 4
		}
		if ext > 0 && ext < len(b) {
			p = ext
		}
	}
	idLen, hdrLen := 4, 10
	if ver == 2 {
		idLen, hdrLen = 3, 6
	}
	for p+hdrLen <= len(b) {
		id := string(b[p : p+idLen])
		if id[0] == 0 {
			break
		}
		var size int
		var fflags byte
		switch ver {
		case 2:
			size = be24(b[p+3 : p+6])
		case 3:
			size = int(be32(b[p+4 : p+8]))
			fflags = b[p+9]
		default:
			size = syncsafe(b[p+4 : p+8])
			fflags = b[p+9]
		}
		p += hdrLen
		if size <= 0 || p+size > len(b) {
			break
		}
		data := b[p : p+size]
		p += size
		if ver == 4 && fflags&0x02 != 0 {
			data = unsync(data)
		}
		if fflags&0x0c != 0 && ver >= 3 { // compressé ou chiffré : ignoré
			continue
		}
		switch {
		case id == "APIC" || id == "PIC":
			t.Cover = true
		case id == "TXXX" || id == "TXX":
			if len(data) < 2 {
				continue
			}
			parts := splitText(data[0], data[1:])
			if len(parts) >= 2 {
				setTag(t, parts[0], parts[1])
			}
		case id[0] == 'T':
			if len(data) < 1 {
				continue
			}
			parts := splitText(data[0], data[1:])
			if len(parts) > 0 {
				setTag(t, id, parts[0])
			}
		}
	}
}

func unsync(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte{0xff, 0x00}, []byte{0xff})
}

// splitText décode un texte ID3 (codage 0 à 3) et le coupe sur les zéros.
func splitText(enc byte, b []byte) []string {
	var s string
	switch enc {
	case 1, 2:
		big := enc == 2
		if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
			big, b = false, b[2:]
		} else if len(b) >= 2 && b[0] == 0xfe && b[1] == 0xff {
			big, b = true, b[2:]
		}
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			v := uint16(b[i])<<8 | uint16(b[i+1])
			if !big {
				v = uint16(b[i]) | uint16(b[i+1])<<8
			}
			if v == 0xfeff && len(u) > 0 && u[len(u)-1] == 0 {
				continue // BOM de la valeur suivante
			}
			u = append(u, v)
		}
		s = string(utf16.Decode(u))
	case 3:
		s = string(b)
	default:
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		s = string(r)
	}
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var mpegRates = [2][3][16]int{
	{ // MPEG-1 : couches I, II, III
		{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0},
		{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},
		{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
	},
	{ // MPEG-2 et 2.5
		{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
		{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
	},
}

var mpegSampleRates = [4][3]int{{11025, 12000, 8000}, {0, 0, 0}, {22050, 24000, 16000}, {44100, 48000, 32000}}

// mpegFrame cherche la première trame MPEG, lit Xing / Info / VBRI pour la
// durée exacte, sinon l'estime au débit constant.
func mpegFrame(t *Track, r io.ReaderAt, n, start int64) error {
	b, err := readAt(r, start, 64<<10)
	if len(b) < 4 {
		return err
	}
	for i := 0; i+4 <= len(b); i++ {
		if b[i] != 0xff || b[i+1]&0xe0 != 0xe0 {
			continue
		}
		verBits, layerBits := b[i+1]>>3&0x03, b[i+1]>>1&0x03
		rateIdx, srIdx := b[i+2]>>4, b[i+2]>>2&0x03
		if verBits == 1 || layerBits == 0 || rateIdx == 0 || rateIdx == 15 || srIdx == 3 {
			continue
		}
		v1 := verBits == 3
		layer := 4 - int(layerBits) // 1, 2, 3
		vi := 1
		if v1 {
			vi = 0
		}
		kbps := mpegRates[vi][layer-1][rateIdx]
		sr := mpegSampleRates[verBits][srIdx]
		mono := b[i+3]>>6 == 3
		t.SampleRate, t.Bitrate = sr, kbps
		t.Channels = 2
		if mono {
			t.Channels = 1
		}
		spf := 1152
		if layer == 1 {
			spf = 384
		} else if layer == 3 && !v1 {
			spf = 576
		}
		if layer != 3 {
			t.Codec = "MP2"
		}
		// Xing / Info après les informations annexes.
		side := 32
		switch {
		case v1 && mono:
			side = 17
		case !v1 && !mono:
			side = 17
		case !v1 && mono:
			side = 9
		}
		x := i + 4 + side
		if x+16 <= len(b) && (string(b[x:x+4]) == "Xing" || string(b[x:x+4]) == "Info") {
			t.VBR = string(b[x:x+4]) == "Xing"
			fl := be32(b[x+4:])
			p := x + 8
			var frames, bytesN int64
			if fl&1 != 0 && p+4 <= len(b) {
				frames = be32(b[p:])
				p += 4
			}
			if fl&2 != 0 && p+4 <= len(b) {
				bytesN = be32(b[p:])
			}
			if frames > 0 && sr > 0 {
				t.Duration = float64(frames*int64(spf)) / float64(sr)
				if bytesN > 0 && t.Duration > 0 {
					t.Bitrate = int(float64(bytesN) * 8 / t.Duration / 1000)
				}
				if !t.VBR {
					t.Bitrate = kbps
				}
				return nil
			}
		}
		if v := i + 4 + 32; v+18 <= len(b) && string(b[v:v+4]) == "VBRI" {
			t.VBR = true
			bytesN, frames := be32(b[v+10:]), be32(b[v+14:])
			if frames > 0 && sr > 0 {
				t.Duration = float64(frames*int64(spf)) / float64(sr)
				if t.Duration > 0 {
					t.Bitrate = int(float64(bytesN) * 8 / t.Duration / 1000)
				}
				return nil
			}
		}
		// Débit constant : durée d'après la taille.
		if kbps > 0 {
			audio := n - start - int64(i)
			if tail, err := readAt(r, n-128, 3); err == nil && string(tail) == "TAG" {
				audio -= 128
			}
			t.Duration = float64(audio) * 8 / float64(kbps*1000)
		}
		return nil
	}
	return errFormat
}

// ---------- MP4 / M4A : atomes ----------

func readMP4(t *Track, r io.ReaderAt, n int64) error {
	var timescale, duration, mdat int64
	var walk func(off, end int64, depth int) error
	walk = func(off, end int64, depth int) error {
		for off+8 <= end && depth < 12 {
			h, err := readAt(r, off, 8)
			if err != nil {
				return err
			}
			size, typ := be32(h), string(h[4:8])
			hdr := int64(8)
			if size == 1 {
				x, err := readAt(r, off+8, 8)
				if err != nil {
					return err
				}
				size, hdr = int64(binary.BigEndian.Uint64(x)), 16
			} else if size == 0 {
				size = end - off
			}
			if size < hdr || off+size > end {
				return nil
			}
			body, bodyEnd := off+hdr, off+size
			switch typ {
			case "moov", "trak", "mdia", "minf", "stbl", "udta", "ilst":
				if err := walk(body, bodyEnd, depth+1); err != nil {
					return err
				}
			case "meta":
				if err := walk(body+4, bodyEnd, depth+1); err != nil {
					return err
				}
			case "mvhd":
				b, err := readAt(r, body, 32)
				if err != nil {
					return err
				}
				if b[0] == 1 {
					timescale, duration = be32(b[20:24]), int64(binary.BigEndian.Uint64(b[24:32]))
				} else {
					timescale, duration = be32(b[12:16]), be32(b[16:20])
				}
			case "stsd":
				b, err := readAt(r, body, 8+36)
				if err != nil {
					return err
				}
				// Première entrée : format, puis description audio.
				switch string(b[12:16]) {
				case "alac":
					t.Codec = "ALAC"
				case "mp4a":
					t.Codec = "AAC"
				}
				// Entrée audio : version, révision, fabricant, voies, bits,
				// compression, paquet, fréquence (16.16).
				e := b[24:]
				t.Channels = be16(e[8:10])
				t.Bits = be16(e[10:12])
				t.SampleRate = be16(e[16:18])
				if t.Codec == "AAC" {
					t.Bits = 0
				}
				if t.Codec == "ALAC" {
					// Le « cookie » ALAC dit la vraie profondeur et la vraie fréquence.
					if c, err := readAt(r, body+8+36+12, 24); err == nil && len(c) == 24 {
						if bits := int(c[5]); bits > 0 {
							t.Bits = bits
						}
						if sr := be32(c[20:24]); sr > 0 {
							t.SampleRate = int(sr)
						}
					}
				}
			case "covr":
				t.Cover = true
			case "mdat":
				mdat += size - hdr
			default:
				if depth > 0 && (typ[0] == 0xa9 || typ == "aART" || typ == "trkn" || typ == "disk" || typ == "----") {
					mp4Item(t, r, typ, body, bodyEnd)
				}
			}
			off += size
		}
		return nil
	}
	if err := walk(0, n, 0); err != nil {
		return err
	}
	if timescale > 0 && duration > 0 {
		t.Duration = float64(duration) / float64(timescale)
	}
	if t.Codec == "AAC" && t.Duration > 0 && mdat > 0 {
		t.Bitrate = int(float64(mdat) * 8 / t.Duration / 1000)
	}
	return nil
}

// mp4Item lit un élément d'ilst (« ©ART », « trkn », « ---- » libre).
func mp4Item(t *Track, r io.ReaderAt, typ string, off, end int64) {
	if end-off > 1<<20 {
		return
	}
	b, err := readAt(r, off, int(end-off))
	if err != nil {
		return
	}
	var name string
	for p := 0; p+8 <= len(b); {
		size := int(be32(b[p:]))
		if size < 8 || p+size > len(b) {
			return
		}
		sub, body := string(b[p+4:p+8]), b[p+8:p+size]
		p += size
		switch sub {
		case "name":
			if len(body) > 4 {
				name = string(body[4:])
			}
		case "data":
			if len(body) < 8 {
				continue
			}
			v := body[8:]
			switch typ {
			case "trkn", "disk":
				if len(v) >= 6 {
					key := "TRACKNUMBER"
					if typ == "disk" {
						key = "DISCNUMBER"
					}
					setTag(t, key, strconv.Itoa(be16(v[2:4]))+"/"+strconv.Itoa(be16(v[4:6])))
				}
			case "----":
				setTag(t, name, string(v))
			case "aART":
				setTag(t, "AART", string(v))
			default: // « ©ART », « ©alb »… (© en un octet, 0xA9)
				setTag(t, "©"+strings.ToUpper(typ[1:]), string(v))
			}
		}
	}
}

// ---------- Ogg : Vorbis et Opus ----------

func readOgg(t *Track, r io.ReaderAt, n int64) error {
	// Les deux premiers paquets (identification, commentaires), qui peuvent
	// s'étaler sur plusieurs pages.
	var packets [][]byte
	var cur []byte
	off := int64(0)
	for len(packets) < 2 && off+27 <= n && off < 32<<20 {
		h, err := readAt(r, off, 27)
		if err != nil || string(h[0:4]) != "OggS" {
			break
		}
		segs := int(h[26])
		lace, err := readAt(r, off+27, segs)
		if err != nil {
			return err
		}
		body := off + 27 + int64(segs)
		total := 0
		for _, l := range lace {
			total += int(l)
		}
		data, err := readAt(r, body, total)
		if err != nil {
			return err
		}
		p := 0
		for _, l := range lace {
			if len(cur) < 16<<20 { // pochette géante dans les commentaires : tronquée
				cur = append(cur, data[p:p+int(l)]...)
			}
			p += int(l)
			if l < 255 {
				packets = append(packets, cur)
				cur = nil
				if len(packets) == 2 {
					break
				}
			}
		}
		off = body + int64(total)
	}
	if len(packets) == 0 {
		return errFormat
	}
	preskip := int64(0)
	rate := 0
	id := packets[0]
	switch {
	case len(id) >= 30 && string(id[1:7]) == "vorbis":
		t.Codec = "Vorbis"
		t.Channels = int(id[11])
		rate = int(le32(id[12:16]))
		t.SampleRate = rate
		if nominal := le32(id[20:24]); nominal > 0 {
			t.Bitrate = int(nominal / 1000)
		}
	case len(id) >= 19 && string(id[0:8]) == "OpusHead":
		t.Codec = "Opus"
		t.Channels = int(id[9])
		preskip = int64(le16(id[10:12]))
		t.SampleRate = int(le32(id[12:16]))
		rate = 48000
	default:
		return errFormat
	}
	if len(packets) > 1 {
		c := packets[1]
		switch {
		case t.Codec == "Vorbis" && len(c) > 7 && string(c[1:7]) == "vorbis":
			vorbisComments(t, c[7:])
		case t.Codec == "Opus" && len(c) > 8 && string(c[0:8]) == "OpusTags":
			vorbisComments(t, c[8:])
		}
	}
	// Durée : position de la dernière page.
	tailLen := int64(64 << 10)
	if tailLen > n {
		tailLen = n
	}
	tail, _ := readAt(r, n-tailLen, int(tailLen))
	if i := bytes.LastIndex(tail, []byte("OggS")); i >= 0 && i+14 <= len(tail) && rate > 0 {
		gran := int64(binary.LittleEndian.Uint64(tail[i+6 : i+14]))
		if gran > preskip {
			t.Duration = float64(gran-preskip) / float64(rate)
		}
	}
	if t.Codec == "Opus" && t.Duration > 0 {
		t.Bitrate = int(float64(n) * 8 / t.Duration / 1000)
	}
	return nil
}
