package fiche_test

import (
	"strings"
	"testing"

	"baratie/internal/fiche"
	"baratie/internal/tracker"
	"baratie/internal/unit3d/fake"
)

// mi : un MediaInfo d'encode, à compléter.
func miEncode(container, format, lib, settings, bitrate, depth string, audios ...string) string {
	b := "General\nFormat                                   : " + container + "\nDuration                                 : 1 h 45 min\n\nVideo\nID                                       : 1\nFormat                                   : " + format +
		"\nWidth                                    : 1 920 pixels\nHeight                                   : 800 pixels\nBit depth                                : " + depth + "\n"
	if bitrate != "" {
		b += "Bit rate                                 : " + bitrate + "\n"
	}
	if lib != "" {
		b += "Writing library                          : " + lib + "\n"
	}
	if settings != "" {
		b += "Encoding settings                        : " + settings + "\n"
	}
	for i, a := range audios {
		b += "\nAudio #" + string(rune('1'+i)) + "\n" + a
	}
	return b
}

const audioFR = "Format                                   : AC-3\nChannel(s)                               : 6 channels\nBit rate                                 : 640 kb/s\nLanguage                                 : French\n"
const audioEN = "Format                                   : DTS\nChannel(s)                               : 6 channels\nBit rate                                 : 1 509 kb/s\nLanguage                                 : English\n"

func qbuild(name string, mi string, files []fiche.File, lang string) fiche.Fiche {
	if files == nil {
		files = []fiche.File{{Rel: name + ".mkv", Size: 8 << 30}}
	}
	return fiche.Build(fiche.Input{Name: name, Files: files, MediaInfo: mi, TMDB: 1, IMDB: 1, OrigLang: lang, Rules: fake.Rules().Fiche()})
}

func TestMediaInfoNumbers(t *testing.T) {
	mi := fiche.ParseMediaInfo(miEncode("Matroska", "AVC", "x264 core 164 r3095", "cabac=1 / rc=crf / crf=18.0", "9 850 kb/s", "8 bits", audioFR, audioEN))
	if mi.VideoKbps != 9850 || mi.Container != "Matroska" || mi.Duration != 6300 || len(mi.Audios) != 2 || mi.Audios[1].Kbps != 1509 || mi.Audios[0].Channels != 6 {
		t.Fatalf("%+v", mi)
	}
	// Débit absent : déduit de la taille du flux et de la durée.
	mi = fiche.ParseMediaInfo("General\nFormat : Matroska\n\nVideo\nFormat : AVC\nWidth : 1 920 pixels\nDuration : 1 h 40 min\nStream size : 6.00 GiB (90%)\n")
	if mi.VideoKbps < 8580 || mi.VideoKbps > 8600 {
		t.Fatalf("débit déduit %d", mi.VideoKbps)
	}
	if fiche.ParseMediaInfo("Video\nBit rate : 24.8 Mb/s\nWidth : 3 840 pixels\n").VideoKbps != 24800 {
		t.Fatal("Mb/s")
	}
}

func TestQualityBluRayEncode(t *testing.T) {
	name := "Film.2020.MULTi.1080p.BluRay.x264-GRP"
	ok := qbuild(name, miEncode("Matroska", "AVC", "x264 core 164", "rc=crf / crf=18.0", "9 850 kb/s", "8 bits", audioFR, audioEN), nil, "en")
	if len(ok.Blockers) != 0 {
		t.Fatalf("encode conforme bloqué : %v", ok.Blockers)
	}
	if !strings.Contains(ok.Fields.Description, "Source : BluRay") {
		t.Fatalf("source absente : %q", ok.Fields.Description)
	}
	for _, c := range []struct {
		mi, lang, want string
	}{
		{miEncode("Matroska", "AVC", "x264 core 164", "rc=crf", "6 500 kb/s", "8 bits", audioFR, audioEN), "en", "sous le minimum"},
		{miEncode("Matroska", "AVC", "x264 core 164", "rc=abr / bitrate=9000", "9 000 kb/s", "8 bits", audioFR, audioEN), "en", "CRF ou le multi-passe"},
		{miEncode("Matroska", "AVC", "NVEncC 7.30", "", "12 000 kb/s", "8 bits", audioFR, audioEN), "en", "GPU"},
		{miEncode("Matroska", "AVC", "x264 core 164", "rc=crf", "9 000 kb/s", "8 bits", audioFR), "en", "langue originale"},
		{miEncode("MPEG-4", "AVC", "x264 core 164", "rc=crf", "9 000 kb/s", "8 bits", audioFR, audioEN), "en", "MP4"},
		{miEncode("Matroska", "AVC", "x264 core 164", "rc=crf", "9 000 kb/s", "8 bits", audioEN), "en", "VO sans sous-titres"},
	} {
		f := qbuild(name, c.mi, nil, c.lang)
		if !has(f.Blockers, c.want) {
			t.Errorf("attendu « %s », bloquants %v", c.want, f.Blockers)
		}
	}
	// Multi-passe x264 : accepté.
	if f := qbuild(name, miEncode("Matroska", "AVC", "x264 core 164", "rc=2pass / bitrate=9000", "9 000 kb/s", "8 bits", audioFR, audioEN), nil, "en"); len(f.Blockers) != 0 {
		t.Fatalf("2 passes : %v", f.Blockers)
	}
	// Animation : seuil plus bas (5000 en 1080p x264).
	f := fiche.Build(fiche.Input{Name: name, Files: []fiche.File{{Rel: name + ".mkv", Size: 4 << 30}}, MediaInfo: miEncode("Matroska", "AVC", "x264 core 164", "rc=crf", "6 000 kb/s", "8 bits", audioFR, audioEN), TMDB: 1, OrigLang: "en", Animation: true, Rules: fake.Rules().Fiche()})
	if has(f.Blockers, "minimum") {
		t.Fatalf("animation : %v", f.Blockers)
	}
}

func TestQualityWEB(t *testing.T) {
	// WEB-DL intact : pas de seuil, pas d'encodeur exigé.
	webdl := miEncode("Matroska", "AVC", "", "", "3 000 kb/s", "8 bits", audioFR, audioEN)
	if f := qbuild("Film.2020.MULTi.1080p.NF.WEB-DL.DDP5.1.H264-GRP", webdl, nil, "en"); len(f.Blockers) != 0 || !strings.Contains(f.Fields.Description, "Source : WEB-DL NF · 1080p · H.264 · 3\u202f000 kb/s") {
		t.Fatalf("WEB-DL : %v %q", f.Blockers, f.Fields.Description)
	}
	// WEBRip x265 1080p sous 3500 kb/s : refusé ; source citée.
	rip := miEncode("Matroska", "HEVC", "x265 4.1", "rc=crf / crf=20", "3 000 kb/s", "10 bits", audioFR, audioEN)
	f := qbuild("Film.2020.MULTi.1080p.AMZN.WEBRip.x265-GRP", rip, nil, "en")
	if !has(f.Blockers, "sous le minimum") || !strings.Contains(f.Fields.Description, "Source : WEB-DL AMZN") || !has(f.Warnings, "10 bits") {
		t.Fatalf("WEBRip : %v %v %q", f.Blockers, f.Warnings, f.Fields.Description)
	}
}

func TestQualitySD(t *testing.T) {
	sd := "General\nFormat : AVI\n\nVideo\nFormat : MPEG-4 Visual\nCodec ID : XVID\nWidth : 720 pixels\nHeight : 304 pixels\nBit rate : 1 100 kb/s\nStream size : 800 MiB\n\n" +
		"Audio\nFormat : AC-3\nChannel(s) : 6 channels\nBit rate : 448 kb/s\nStream size : 300 MiB\nLanguage : French\n"
	name := "Navet.1987.FRENCH.DVDRip.XviD-VHS"
	f := qbuild(name, sd, []fiche.File{{Rel: name + ".avi", Size: 1100 << 20}}, "fr")
	if len(f.Blockers) != 0 || !has(f.Warnings, "Helpdesk") || !strings.Contains(f.Fields.Description, "Source : DVD") {
		t.Fatalf("DVDRip XviD : %v %v %q", f.Blockers, f.Warnings, f.Fields.Description)
	}
	// AC3 640, audio plus lourd que la vidéo.
	bad := strings.Replace(strings.Replace(sd, "448 kb/s", "640 kb/s", 1), "300 MiB", "900 MiB", 1)
	if f := qbuild(name, bad, []fiche.File{{Rel: name + ".avi", Size: 1700 << 20}}, "fr"); !has(f.Blockers, "448") || !has(f.Blockers, "plus que la vidéo") {
		t.Fatalf("AC3 640 : %v", f.Blockers)
	}
	// XviD en HD : refusé.
	hd := strings.Replace(strings.Replace(sd, "720 pixels", "1 280 pixels", 1), "304 pixels", "544 pixels", 1)
	if f := qbuild("Navet.1987.FRENCH.720p.BluRay.XviD-VHS", hd, []fiche.File{{Rel: "a.avi", Size: 1 << 30}}, "fr"); !has(f.Blockers, "XviD") || !has(f.Blockers, "AVI") {
		t.Fatalf("XviD HD : %v", f.Blockers)
	}
}

func TestQualityFilesAndTokens(t *testing.T) {
	name := "Film.2020.MULTi.1080p.NF.WEB-DL.H264-GRP"
	mi := miEncode("Matroska", "AVC", "", "", "", "8 bits", audioFR, audioEN)
	files := []fiche.File{{Rel: name + "/" + name + ".mkv", Size: 4 << 30}, {Rel: name + "/Sample/sample.mkv", Size: 50 << 20}, {Rel: name + "/" + name + ".srt", Size: 80 << 10}, {Rel: name + "/" + name + ".nfo", Size: 2 << 10}}
	f := qbuild(name, mi, files, "en")
	if !has(f.Blockers, "sample.mkv") || !has(f.Blockers, ".srt") {
		t.Fatalf("fichiers : %v", f.Blockers)
	}
	if f := qbuild("Film.2020.FRENCH.TS.x264-GRP", "", nil, ""); !has(f.Blockers, "Telesync") {
		t.Fatalf("TS : %v", f.Blockers)
	}
	// « Cam » dans le titre n'est pas une source.
	if f := qbuild("The.Cam.Girl.2020.MULTi.1080p.NF.WEB-DL.H264-GRP", mi, nil, "en"); has(f.Blockers, "CAM") {
		t.Fatalf("titre : %v", f.Blockers)
	}
	if f := qbuild("Film.2020.MULTi.1080p.UHDLight.x265-GRP", "", nil, ""); !has(f.Blockers, "UHDLIGHT") {
		t.Fatalf("UHDLight : %v", f.Blockers)
	}
}

func TestDupes(t *testing.T) {
	name := "Film.2020.MULTi.1080p.AMZN.WEB-DL.DDP5.1.H264-GRP"
	mi := miEncode("Matroska", "AVC", "", "", "", "8 bits", audioFR, audioEN)
	ex := []tracker.Version{
		{ID: "1", Name: "Film.2020.MULTi.1080p.AMZN.WEB-DL.DDP5.1.H264-AUTRE", Type: "WEB", Resolution: "1080p", Size: 8 << 30, Category: "Films"},
		{ID: "2", Name: "Film.2020.MULTi.1080p.NF.WEB-DL.DDP5.1.H264-AUTRE", Type: "WEB", Resolution: "1080p", Size: 8 << 30, Category: "Films"},
		{ID: "3", Name: "Film.2020.MULTi.1080p.WEB.H264-AUTRE", Type: "WEB", Resolution: "1080p", Size: 8 << 30, Category: "Films"},
		{ID: "4", Name: "Film.2020.MULTi.1080p.BluRay.x264-AUTRE", Type: "Encode", Resolution: "1080p", Size: 8 << 30, Category: "Films"},
		{ID: "5", Name: "Film.2020.VOSTFR.1080p.AMZN.WEB-DL.H264-AUTRE", Type: "WEB", Resolution: "1080p", Size: 8 << 30, Category: "Films VOSTFR"},
	}
	f := fiche.Build(fiche.Input{Name: name, Files: []fiche.File{{Rel: name + ".mkv", Size: 8 << 30}}, MediaInfo: mi, TMDB: 1, OrigLang: "en", Existing: ex, Rules: fake.Rules().Fiche()})
	got := map[string]string{}
	for _, v := range f.Versions {
		got[v.ID] = v.Dupe
	}
	want := map[string]string{"1": fiche.DupeSame, "2": "", "3": fiche.DupeMaybe, "4": "", "5": ""}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("version %s : %q, attendu %q", id, got[id], w)
		}
	}
	if !has(f.Warnings, "doublon selon les règles") {
		t.Fatalf("%v", f.Warnings)
	}
	// Bien plus lourde : doublon possible (amélioration à juger).
	f = fiche.Build(fiche.Input{Name: name, Files: []fiche.File{{Rel: name + ".mkv", Size: 12 << 30}}, MediaInfo: mi, TMDB: 1, OrigLang: "en", Existing: ex[:1], Rules: fake.Rules().Fiche()})
	if f.Versions[0].Dupe != fiche.DupeMaybe || !strings.Contains(f.Versions[0].DupeWhy, "50 %") {
		t.Fatalf("%+v", f.Versions[0])
	}
}

func TestVersionIntegrale(t *testing.T) {
	r := fiche.Parse("Les.Bourreaux.Meurent.Aussi.Version.Integrale.1943.VOSTFR.DVDRiP.XViD.AC3-Bandix.avi")
	if r.TV || r.Title != "Les Bourreaux Meurent Aussi" || r.Year != "1943" || r.Codec != "XviD" || r.Source != "DVD" {
		t.Fatalf("%+v", r)
	}
	for _, n := range []string{"Kaamelott.Integrale.FRENCH.1080p.BluRay.x264-GRP", "Film.Serie.S01.FRENCH.1080p.WEB.x264-GRP", "Show.2019.Saison.2.FRENCH.720p.HDTV.x264-GRP", "Show.2019.S02E05.MULTi.1080p.WEB.H264-GRP"} {
		if !fiche.Parse(n).TV {
			t.Errorf("%s : série non vue", n)
		}
	}
}

func TestRadarrNames(t *testing.T) {
	r := fiche.Parse("Inception (2010) Bluray-1080p.mkv")
	if r.Source != "BluRay" || r.Res != "1080p" || r.Group != "" || r.Year != "2010" {
		t.Fatalf("%+v", r)
	}
	if r := fiche.Parse("Dune (2021) Remux-2160p.mkv"); !r.Remux || r.Res != "2160p" {
		t.Fatalf("%+v", r)
	}
	if fiche.LangCode("English") != "en" || fiche.LangCode("Klingon") != "" {
		t.Fatal("LangCode")
	}
}
