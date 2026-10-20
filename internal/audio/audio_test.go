package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// ---------- fabrication de fichiers minimaux ----------

func u32be(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func u32le(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func u16le(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
func u16be(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func u64le(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }

func comments(kv ...string) []byte {
	var b bytes.Buffer
	b.Write(u32le(5))
	b.WriteString("D3X 1")
	b.Write(u32le(uint32(len(kv))))
	for _, s := range kv {
		b.Write(u32le(uint32(len(s))))
		b.WriteString(s)
	}
	return b.Bytes()
}

func flacFile(rate, bits, channels int, samples int64, kv ...string) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	si := make([]byte, 34)
	si[10] = byte(rate >> 12)
	si[11] = byte(rate >> 4)
	si[12] = byte(rate<<4) | byte((channels-1)<<1) | byte((bits-1)>>4)
	si[13] = byte((bits-1)<<4) | byte(samples>>32&0x0f)
	binary.BigEndian.PutUint32(si[14:18], uint32(samples))
	b.Write([]byte{0x00, 0, 0, 34})
	b.Write(si)
	c := comments(kv...)
	b.Write([]byte{0x04, byte(len(c) >> 16), byte(len(c) >> 8), byte(len(c))})
	b.Write(c)
	pic := make([]byte, 40)
	b.Write([]byte{0x86, 0, 0, byte(len(pic))}) // PICTURE, dernier bloc
	b.Write(pic)
	b.Write(make([]byte, 1000)) // « audio »
	return b.Bytes()
}

func id3Frame(id string, data []byte) []byte {
	s := len(data)
	ss := []byte{byte(s >> 21 & 0x7f), byte(s >> 14 & 0x7f), byte(s >> 7 & 0x7f), byte(s & 0x7f)}
	return append(append(append([]byte(id), ss...), 0, 0), data...)
}

func utf16Text(s string) []byte {
	out := []byte{1, 0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func mp3File(xing bool, frames uint32, audioBytes int, frames2 ...[]byte) []byte {
	var tag bytes.Buffer
	tag.Write(id3Frame("TPE1", append([]byte{3}, "Artiste Démo"...)))
	tag.Write(id3Frame("TALB", utf16Text("Album Démo")))
	tag.Write(id3Frame("TDRC", append([]byte{3}, "2021-03-05"...)))
	tag.Write(id3Frame("TRCK", append([]byte{0}, "2/10"...)))
	tag.Write(id3Frame("TXXX", append([]byte{3}, "MusicBrainz Album Id\x0012345678-1234-1234-1234-123456789abc"...)))
	tag.Write(id3Frame("APIC", []byte{0, 'i', 'm', 'a', 'g', 'e', '/', 'j', 'p', 'e', 'g', 0, 3, 0, 1, 2}))
	for _, f := range frames2 {
		tag.Write(f)
	}
	tag.Write(make([]byte, 20)) // bourrage
	s := tag.Len()
	var b bytes.Buffer
	b.WriteString("ID3")
	b.Write([]byte{4, 0, 0, byte(s >> 21 & 0x7f), byte(s >> 14 & 0x7f), byte(s >> 7 & 0x7f), byte(s & 0x7f)})
	b.Write(tag.Bytes())
	// Trame MPEG-1 couche III, 320 kbit/s, 44,1 kHz, stéréo.
	frame := make([]byte, 1044)
	copy(frame, []byte{0xff, 0xfb, 0xe0, 0x00})
	if xing {
		copy(frame[4+32:], "Xing")
		copy(frame[4+32+4:], u32be(3))
		copy(frame[4+32+8:], u32be(frames))
		copy(frame[4+32+12:], u32be(uint32(audioBytes)))
	}
	b.Write(frame)
	b.Write(make([]byte, audioBytes-len(frame)))
	return b.Bytes()
}

func atom(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	return append(append(u32be(uint32(8+len(body))), typ...), body...)
}

func ilstText(typ, v string) []byte {
	return atom(typ, atom("data", u32be(1), u32be(0), []byte(v)))
}

func m4aFile() []byte {
	mvhd := make([]byte, 100)
	copy(mvhd[12:], u32be(44100))
	copy(mvhd[16:], u32be(44100*200)) // 200 s
	entry := make([]byte, 36)
	copy(entry[0:], u32be(36+36))
	copy(entry[4:], "alac")
	copy(entry[16+8:], u16be(2))
	copy(entry[16+10:], u16be(16))
	copy(entry[16+16:], u16be(44100))
	cookie := make([]byte, 24)
	cookie[5] = 24
	copy(cookie[20:], u32be(96000))
	stsd := atom("stsd", u32be(0), u32be(1), entry, atom("alac", u32be(0), cookie))
	free := atom("----", atom("mean", u32be(0), []byte("com.apple.iTunes")), atom("name", u32be(0), []byte("MusicBrainz Album Id")), atom("data", u32be(1), u32be(0), []byte("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")))
	trkn := atom("trkn", atom("data", u32be(0), u32be(0), []byte{0, 0, 0, 7, 0, 12, 0, 0}))
	ilst := atom("ilst", ilstText("\xa9ART", "Artiste M4A"), ilstText("aART", "Artiste Album"), ilstText("\xa9alb", "Album M4A"), ilstText("\xa9day", "2019"), trkn, free, atom("covr", atom("data", u32be(13), u32be(0), []byte{1, 2})))
	moov := atom("moov", atom("mvhd", mvhd), atom("trak", atom("mdia", atom("minf", atom("stbl", stsd)))), atom("udta", atom("meta", u32be(0), atom("hdlr", make([]byte, 25)), ilst)))
	return bytes.Join([][]byte{atom("ftyp", []byte("M4A "), u32be(0)), moov, atom("mdat", make([]byte, 5000))}, nil)
}

func oggPage(gran uint64, packets ...[]byte) []byte {
	var lace []byte
	var body []byte
	for _, p := range packets {
		n := len(p)
		for n >= 255 {
			lace = append(lace, 255)
			n -= 255
		}
		lace = append(lace, byte(n))
		body = append(body, p...)
	}
	h := []byte("OggS")
	h = append(h, 0, 0)
	h = append(h, make([]byte, 8)...)
	binary.LittleEndian.PutUint64(h[6:14], gran)
	h = append(h, make([]byte, 12)...) // série, numéro, CRC (non vérifié)
	h = append(h, byte(len(lace)))
	return append(append(h, lace...), body...)
}

func opusFile() []byte {
	head := append([]byte("OpusHead"), 1, 2)
	head = append(head, u16le(312)...)
	head = append(head, u32le(44100)...)
	head = append(head, 0, 0, 0)
	big := make([]byte, 600) // commentaire sur plusieurs segments
	tags := append([]byte("OpusTags"), comments("ARTIST=Artiste Opus", "ALBUM=Album Opus", "DATE=2020", "TRACKNUMBER=1", "COMMENT="+string(big))...)
	return bytes.Join([][]byte{oggPage(0, head), oggPage(0, tags), oggPage(48000*180+312, make([]byte, 3000))}, nil)
}

func wavFile() []byte {
	fmtc := append(u16le(1), u16le(2)...)
	fmtc = append(fmtc, u32le(48000)...)
	fmtc = append(fmtc, u32le(48000*2*3)...)
	fmtc = append(fmtc, u16le(6)...)
	fmtc = append(fmtc, u16le(24)...)
	data := make([]byte, 48000*2*3*2) // 2 s
	var b bytes.Buffer
	b.WriteString("RIFF")
	b.Write(u32le(uint32(4 + 8 + len(fmtc) + 8 + len(data))))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	b.Write(u32le(uint32(len(fmtc))))
	b.Write(fmtc)
	b.WriteString("data")
	b.Write(u32le(uint32(len(data))))
	b.Write(data)
	return b.Bytes()
}

func dsfFile() []byte {
	var b bytes.Buffer
	b.WriteString("DSD ")
	b.Write(u64le(28))
	b.Write(make([]byte, 16))
	b.WriteString("fmt ")
	b.Write(u64le(52))
	b.Write(u32le(1))
	b.Write(u32le(0))
	b.Write(u32le(2))
	b.Write(u32le(2))
	b.Write(u32le(2822400))
	b.Write(u32le(1))
	b.Write(u64le(2822400 * 60))
	b.Write(u32le(4096))
	b.Write(u32le(0))
	return b.Bytes()
}

func write(t *testing.T, dir, name string, b []byte) File {
	t.Helper()
	p := filepath.Join(dir, name)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return File{Rel: "Album/" + filepath.ToSlash(name), Abs: p, Size: int64(len(b))}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

// ---------- tests ----------

func TestFormats(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		data []byte
		chk  func(t *testing.T, tr Track)
	}{
		{"01.flac", flacFile(96000, 24, 2, 96000*245, "ARTIST=Artiste", "ALBUM=Album FLAC", "DATE=2018-02-01", "TRACKNUMBER=1/9", "MUSICBRAINZ_ALBUMID=0F0E0D0C-0B0A-0908-0706-050403020100", "LABEL=Label Démo"), func(t *testing.T, tr Track) {
			if tr.Codec != "FLAC" || !tr.Lossless || tr.Bits != 24 || tr.SampleRate != 96000 || tr.Channels != 2 || !near(tr.Duration, 245) || !tr.Cover {
				t.Fatalf("%+v", tr)
			}
			if tr.Tags["album"] != "Album FLAC" || tr.Tags["label"] != "Label Démo" || tr.Tags["mb_albumid"] == "" {
				t.Fatalf("tags %v", tr.Tags)
			}
		}},
		{"02.mp3", mp3File(true, 7656, 2_000_000), func(t *testing.T, tr Track) {
			// 7656 trames de 1152 échantillons à 44,1 kHz = 200 s.
			if tr.Codec != "MP3" || tr.Lossless || !tr.VBR || !near(tr.Duration, 200) || tr.Bitrate != 80 || !tr.Cover {
				t.Fatalf("%+v", tr)
			}
			if tr.Tags["artist"] != "Artiste Démo" || tr.Tags["album"] != "Album Démo" || tr.Tags["date"] != "2021-03-05" || tr.Tags["track"] != "2/10" || tr.Tags["mb_albumid"] != "12345678-1234-1234-1234-123456789abc" {
				t.Fatalf("tags %v", tr.Tags)
			}
		}},
		{"03.mp3", mp3File(false, 0, 400_000), func(t *testing.T, tr Track) {
			// Débit constant 320 kbit/s : durée d'après la taille.
			if tr.VBR || tr.Bitrate != 320 || tr.Duration < 9.9 || tr.Duration > 10.1 {
				t.Fatalf("%+v", tr)
			}
		}},
		{"04.m4a", m4aFile(), func(t *testing.T, tr Track) {
			if tr.Codec != "ALAC" || !tr.Lossless || tr.Bits != 24 || tr.SampleRate != 96000 || tr.Channels != 2 || !near(tr.Duration, 200) || !tr.Cover {
				t.Fatalf("%+v", tr)
			}
			if tr.Tags["artist"] != "Artiste M4A" || tr.Tags["albumartist"] != "Artiste Album" || tr.Tags["album"] != "Album M4A" || tr.Tags["date"] != "2019" || tr.Tags["track"] != "7/12" || tr.Tags["mb_albumid"] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
				t.Fatalf("tags %v", tr.Tags)
			}
		}},
		{"05.opus", opusFile(), func(t *testing.T, tr Track) {
			if tr.Codec != "Opus" || tr.Lossless || tr.SampleRate != 44100 || !near(tr.Duration, 180) {
				t.Fatalf("%+v", tr)
			}
			if tr.Tags["artist"] != "Artiste Opus" || tr.Tags["album"] != "Album Opus" {
				t.Fatalf("tags %v", tr.Tags)
			}
		}},
		{"06.wav", wavFile(), func(t *testing.T, tr Track) {
			if tr.Codec != "WAV" || !tr.Lossless || tr.Bits != 24 || tr.SampleRate != 48000 || !near(tr.Duration, 2) {
				t.Fatalf("%+v", tr)
			}
		}},
		{"07.dsf", dsfFile(), func(t *testing.T, tr Track) {
			if tr.Codec != "DSD" || tr.SampleRate != 2822400 || !near(tr.Duration, 60) {
				t.Fatalf("%+v", tr)
			}
		}},
	}
	for _, c := range cases {
		f := write(t, dir, c.name, c.data)
		tr := ReadFile(f.Abs, f.Rel)
		if tr.Error != "" {
			t.Fatalf("%s : %s", c.name, tr.Error)
		}
		t.Run(c.name, func(t *testing.T) { c.chk(t, tr) })
	}
}

// Un fichier abîmé ou tronqué ne fait jamais planter : il signale son erreur.
func TestBroken(t *testing.T) {
	dir := t.TempDir()
	good := map[string][]byte{"a.flac": flacFile(44100, 16, 2, 44100*10, "ARTIST=x"), "b.mp3": mp3File(true, 100, 10000), "c.m4a": m4aFile(), "d.opus": opusFile(), "e.wav": wavFile(), "f.dsf": dsfFile()}
	for name, b := range good {
		for _, cut := range []int{0, 3, 11, 40, len(b) / 2} {
			f := write(t, dir, "cut"+name, b[:cut])
			_ = ReadFile(f.Abs, f.Rel)
		}
		junk := append([]byte(nil), b...)
		for i := 8; i < len(junk); i += 7 {
			junk[i] = 0xff
		}
		f := write(t, dir, "junk"+name, junk)
		_ = ReadFile(f.Abs, f.Rel)
	}
}

func TestAlbum(t *testing.T) {
	dir := t.TempDir()
	var files []File
	// Pistes dans le désordre, deux disques, pochette à côté.
	for _, tr := range []struct {
		name, disc, track string
	}{{"CD2/01.flac", "2", "1"}, {"CD1/02.flac", "1", "2"}, {"CD1/01.flac", "1", "1"}} {
		files = append(files, write(t, dir, tr.name, flacFile(44100, 16, 2, 44100*100,
			"ARTIST=Artiste", "ALBUMARTIST=Artiste", "ALBUM=Le Disque", "DATE=2015", "ORIGINALDATE=1999",
			"DISCNUMBER="+tr.disc, "TRACKNUMBER="+tr.track, "MUSICBRAINZ_ALBUMID=11111111-2222-3333-4444-555555555555", "MEDIA=CD")))
	}
	files = append(files, write(t, dir, "cover.jpg", []byte{1}))
	a := Analyze(files)
	if a == nil || len(a.Tracks) != 3 {
		t.Fatalf("%+v", a)
	}
	if a.Tracks[0].Rel != "Album/CD1/01.flac" || a.Tracks[2].Rel != "Album/CD2/01.flac" {
		t.Fatalf("ordre : %s, %s, %s", a.Tracks[0].Rel, a.Tracks[1].Rel, a.Tracks[2].Rel)
	}
	if a.Artist != "Artiste" || a.Title != "Le Disque" || a.Year != "2015" || a.Media != "CD" || a.Discs != 2 || !a.Cover || a.Various || a.Mixed {
		t.Fatalf("%+v", a)
	}
	if a.MBRelease != "11111111-2222-3333-4444-555555555555" || a.Token != "FLAC 16bit-44.1kHz" || !near(a.Duration, 300) {
		t.Fatalf("%s / %s / %f", a.MBRelease, a.Token, a.Duration)
	}

	// Compilation : un artiste par piste, pas d'artiste d'album.
	var comp []File
	for _, art := range []string{"Un", "Deux", "Trois", "Quatre"} {
		comp = append(comp, write(t, dir, "comp/"+art+".mp3", mp3File(false, 0, 100_000, id3Frame("TPE1", append([]byte{3}, art...)))))
	}
	c := Analyze(comp)
	if c == nil || c.Token != "MP3 320" {
		t.Fatalf("%+v", c)
	}
	if Analyze([]File{write(t, dir, "film.mkv", []byte{1})}) != nil {
		t.Fatal("album sans piste audio")
	}
}

func TestToken(t *testing.T) {
	for _, c := range []struct {
		a    Album
		want string
	}{
		{Album{Codec: "FLAC", Lossless: true, Bits: 24, SampleRate: 96000}, "FLAC 24bit-96kHz"},
		{Album{Codec: "FLAC", Lossless: true, Bits: 24, SampleRate: 88200}, "FLAC 24bit-88.2kHz"},
		{Album{Codec: "ALAC", Lossless: true, Bits: 16, SampleRate: 44100}, "ALAC 16bit-44.1kHz"},
		{Album{Codec: "DSD", Lossless: true, SampleRate: 5644800}, "DSD128"},
		{Album{Codec: "MP3", Bitrate: 319}, "MP3 320"},
		{Album{Codec: "MP3", Bitrate: 245, VBR: true}, "MP3 VBR"},
		{Album{Codec: "AAC", Bitrate: 262}, "AAC 256"},
		{Album{Codec: "FLAC", Lossless: true}, ""},
	} {
		if got := token(&c.a); got != c.want {
			t.Errorf("%+v : %q, attendu %q", c.a, got, c.want)
		}
	}
}
