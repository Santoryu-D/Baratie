package fiche_test

import (
	"strings"
	"testing"

	"baratie/internal/fiche"
	"baratie/internal/tracker"
	"baratie/internal/unit3d/fake"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in                                             string
		title, year, lang, langVar, res, src, codec, g string
		remux                                          bool
	}{
		{"Inception.2010.MULTi.1080p.WEB.H265-CHiLL", "Inception", "2010", "MULTi", "", "1080p", "WEB", "H265", "CHiLL", false},
		{"Sans.Soleil.2021.FRENCH.VFF.1080p.WEB.H264-BraD", "Sans Soleil", "2021", "FRENCH", "VFF", "1080p", "WEB", "H264", "BraD", false},
		{"Dune.Part.Two.2024.MULTi.VFF.2160p.UHD.BluRay.REMUX.HDR.DV.TrueHD.Atmos.7.1.HEVC-GRP", "Dune Part Two", "2024", "MULTi", "VFF", "2160p", "BluRay", "HEVC", "GRP", true},
		{"Le.Comte.de.Monte-Cristo.2024.MULTi.1080p.WEB-DL.DDP5.1.H264-GRP.mkv", "Le Comte de Monte-Cristo", "2024", "MULTi", "", "1080p", "WEB", "H264", "GRP", false},
		{"2012.2009.TRUEFRENCH.720p.BluRay.x264-AiRLiNE", "2012", "2009", "TRUEFRENCH", "", "720p", "BluRay", "x264", "AiRLiNE", false},
		{"Taxi.1998.FRENCH.1080p.BluRay.x264-PopHD", "Taxi", "1998", "FRENCH", "", "1080p", "BluRay", "x264", "PopHD", false},
		{"Oppenheimer.2023.VOSTFR.1080p.AMZN.WEB-DL.DDP5.1.H264-FW", "Oppenheimer", "2023", "VOSTFR", "", "1080p", "WEB", "H264", "FW", false},
		{"Les.Visiteurs.1993.MULTi.VFF.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP", "Les Visiteurs", "1993", "MULTi", "VFF", "1080p", "BluRay", "AVC", "GRP", true},
		{"La.Haine.1995.FRENCH.HDTV.720p.x264-NoTag", "La Haine", "1995", "FRENCH", "", "720p", "HDTV", "x264", "NoTag", false},
		{"Amelie.2001.FRENCH.DVDRip.XviD-GRP", "Amelie", "2001", "FRENCH", "", "", "DVD", "XviD", "GRP", false},
		{"Film Sans Point 2020 MULTi 1080p WEB x264-GRP", "Film Sans Point", "2020", "MULTi", "", "1080p", "WEB", "x264", "GRP", false},
	} {
		r := fiche.Parse(c.in)
		got := []string{r.Title, r.Year, r.Lang, r.LangVar, r.Res, r.Source, r.Codec, r.Group}
		want := []string{c.title, c.year, c.lang, c.langVar, c.res, c.src, c.codec, c.g}
		if strings.Join(got, "|") != strings.Join(want, "|") || r.Remux != c.remux {
			t.Errorf("%s\n  lu     %v remux=%v\n  attendu %v remux=%v", c.in, got, r.Remux, want, c.remux)
		}
	}
}

const miWeb1080 = `General
Complete name                            : Inception.2010.MULTi.1080p.WEB.H265-CHiLL.mkv
Format                                   : Matroska

Video
ID                                       : 1
Format                                   : HEVC
Width                                    : 1 920 pixels
Height                                   : 800 pixels
Display aspect ratio                     : 2.40:1
Scan type                                : Progressive
Bit depth                                : 8 bits

Audio #1
ID                                       : 2
Format                                   : E-AC-3
Commercial name                          : Dolby Digital Plus
Channel(s)                               : 6 channels
Language                                 : French
Default                                  : Yes

Audio #2
ID                                       : 3
Format                                   : E-AC-3
Channel(s)                               : 6 channels
Language                                 : English
Default                                  : No

Text #1
ID                                       : 4
Format                                   : UTF-8
Language                                 : French
`

const miVO = `General
Format                                   : Matroska

Video
Format                                   : AVC
Width                                    : 1 280 pixels
Height                                   : 534 pixels
Writing library                          : x264 core 164

Audio
Format                                   : AC-3
Channel(s)                               : 2 channels
Language                                 : English

Text
Language                                 : French
`

func TestMediaInfo(t *testing.T) {
	mi := fiche.ParseMediaInfo(miWeb1080)
	if mi.Resolution() != "1080p" || !mi.HasFrenchAudio() || !mi.HasFrenchSubs() || len(mi.AudioLangs) != 2 || mi.Channels != "5.1" || mi.AudioFmt != "Dolby Digital Plus" {
		t.Fatalf("%+v", mi)
	}
	vo := fiche.ParseMediaInfo(miVO)
	if vo.Resolution() != "720p" || vo.HasFrenchAudio() || !vo.HasFrenchSubs() || vo.Library != "x264" {
		t.Fatalf("%+v", vo)
	}
	if !fiche.ParseMediaInfo("").Empty {
		t.Fatal("MediaInfo vide non signalé")
	}
}

func build(name, mi string, tmdb int, files []fiche.File, existing ...tracker.Version) fiche.Fiche {
	if files == nil {
		files = []fiche.File{{Rel: name + ".mkv", Size: 3 << 30}}
	}
	return fiche.Build(fiche.Input{Name: name, Files: files, MediaInfo: mi, TMDB: tmdb, IMDB: 1375666, Existing: existing, Rules: fake.Rules().Fiche()})
}

func TestBuildWEB(t *testing.T) {
	f := build("Inception.2010.MULTi.1080p.WEB.H265-CHiLL", miWeb1080, 27205, nil)
	if len(f.Blockers) != 0 || len(f.Warnings) != 0 || len(f.Conform) != 0 || f.Suggested != "" {
		t.Fatalf("bloquants %v, alertes %v, conformité %v %q", f.Blockers, f.Warnings, f.Conform, f.Suggested)
	}
	x := f.Fields
	if x.CategoryID != fake.CatFilms || x.TypeID != fake.TypeWEB || x.ResolutionID != 3 || x.SD || x.TMDB != 27205 || x.IMDB != 1375666 || x.TVDB != 0 || x.Anonymous {
		t.Fatalf("%+v", x)
	}
	if x.Description == "" || !strings.Contains(x.Description, "Inception.2010") || x.MediaInfo == "" {
		t.Fatalf("description %q", x.Description)
	}
}

func TestBuildDeductions(t *testing.T) {
	// Encode BluRay, VOSTFR dans le nom.
	f := build("Film.2020.VOSTFR.720p.BluRay.x264-GRP", miVO, 1, nil)
	if f.Fields.TypeID != fake.TypeEncode || f.Fields.CategoryID != fake.CatFilmsVOSTFR || f.Fields.ResolutionID != 5 {
		t.Fatalf("%+v %v", f.Fields, f.Blockers)
	}
	// Pas de piste française, sous-titres FR, nom sans VOSTFR : rangé en VOSTFR, avec alerte.
	f = build("Film.2020.MULTi.720p.BluRay.x264-GRP", miVO, 1, nil)
	if f.Fields.CategoryID != fake.CatFilmsVOSTFR || len(f.Warnings) < 2 {
		t.Fatalf("%+v %v", f.Fields, f.Warnings)
	}
	// Remux.
	if f := build("Film.2020.MULTi.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP", "", 1, nil); f.Fields.TypeID != fake.TypeRemux {
		t.Fatalf("remux : %+v", f.Fields)
	}
	// HDTV, SD.
	if f := build("Film.1995.FRENCH.576p.HDTV.x264-GRP", "", 1, nil); f.Fields.TypeID != fake.TypeHDTV || !f.Fields.SD || f.Fields.ResolutionID != 6 {
		t.Fatalf("hdtv : %+v", f.Fields)
	}
	// Full Disc : bloqué (BDInfo).
	files := []fiche.File{{Rel: "BDMV/STREAM/00001.m2ts", Size: 30 << 30}}
	if f := build("Film.2020.COMPLETE.UHD.BLURAY-GRP", "", 1, files); f.TypeKind != fiche.TypeFullDisc || !has(f.Blockers, "BDInfo") {
		t.Fatalf("full disc : %v", f.Blockers)
	}
	// Résolution du nom contredite par l'image : alerte, l'image fait foi.
	if f := build("Inception.2010.MULTi.720p.WEB.H265-CHiLL", miWeb1080, 1, nil); f.Fields.ResolutionID != 3 || !has(f.Warnings, "le nom dit 720p") {
		t.Fatalf("résolution : %+v %v", f.Fields, f.Warnings)
	}
	// Série : refusée au MVP.
	if f := build("Serie.S01E02.MULTi.1080p.WEB.H264-GRP", "", 1, nil); !has(f.Blockers, "série") {
		t.Fatalf("série : %v", f.Blockers)
	}
	// Pas de TMDB : bloqué.
	if f := build("Inception.2010.MULTi.1080p.WEB.H265-CHiLL", miWeb1080, 0, nil); !has(f.Blockers, "TMDB") {
		t.Fatalf("tmdb : %v", f.Blockers)
	}
}

func TestBuildTeams(t *testing.T) {
	if f := build("Film.2020.MULTi.1080p.WEB.H264-BADTEAM", "", 1, nil); !has(f.Blockers, "team interdite") {
		t.Fatalf("%v", f.Blockers)
	}
	if f := build("Film.2020.MULTi.1080p.WEB.H264-badteam", "", 1, nil); !has(f.Blockers, "team interdite") {
		t.Fatalf("casse : %v", f.Blockers)
	}
	if f := build("Film.2020.MULTi.1080p.BluRay.x264-BRUT", "", 1, nil); !has(f.Blockers, "qu'en brut") {
		t.Fatalf("BRUT encode : %v", f.Blockers)
	}
	if f := build("Film.2020.MULTi.1080p.BluRay.REMUX.AVC-BRUT", "", 1, nil); has(f.Blockers, "brut") {
		t.Fatalf("BRUT remux : %v", f.Blockers)
	}
	// BADTEAMS n'est pas BADTEAM.
	if f := build("Film.2020.MULTi.1080p.WEB.H264-BADTEAMS", "", 1, nil); has(f.Blockers, "interdite") {
		t.Fatalf("faux positif : %v", f.Blockers)
	}
}

func TestBuildExisting(t *testing.T) {
	files := []fiche.File{{Rel: "Film.mkv", Size: 3_792_416_704}, {Rel: "Film.nfo", Size: 4000}}
	other := tracker.Version{ID: "1", Name: "Film.2021.MULTi.2160p.WEB.H265-AUTRE", Files: []int64{9 << 30}}
	same := tracker.Version{ID: "2", Name: "Film.2021.FRENCH.1080p.WEB.H264-Renomme", Files: []int64{3_792_416_704}}
	f := build("Film.2021.FRENCH.1080p.WEB.H264-BraD", "", 1, files, other)
	if len(f.Blockers) != 0 || len(f.Versions) != 1 || f.Versions[0].Identical {
		t.Fatalf("autre version seulement : %v %+v", f.Blockers, f.Versions)
	}
	f = build("Film.2021.FRENCH.1080p.WEB.H264-BraD", "", 1, files, other, same)
	if !has(f.Blockers, "à l'identique") || !f.Versions[1].Identical {
		t.Fatalf("même release : %v", f.Blockers)
	}
	byName := tracker.Version{ID: "3", Name: "Film 2021 FRENCH 1080p WEB H264-BraD"}
	if f := build("Film.2021.FRENCH.1080p.WEB.H264-BraD", "", 1, files, byName); !has(f.Blockers, "de ce nom") {
		t.Fatalf("même nom : %v", f.Blockers)
	}
}

func TestConformity(t *testing.T) {
	for _, c := range []struct{ in, sugg, issue string }{
		{"Inception.2010.MULTi.1080p.WEB.H265-CHiLL", "", ""},
		{"Inception.2010.1080p.MULTi.WEB.H265-CHiLL", "Inception.2010.MULTi.1080p.WEB.H265-CHiLL", "avant la résolution"},
		{"Inception.2010.1080p.WEB.H265-CHiLL", "", "langue absente"},
		{"Inception.2010.MULTi.1080p.WEB.H265", "", "team absente"},
		{"Film.2020.MULTi.REPACK.1080p.WEB.H264-GRP", "", ""},
		{"Film.2020.REPACK.1080p.FRENCH.BluRay.x264-GRP", "Film.2020.REPACK.FRENCH.1080p.BluRay.x264-GRP", "avant la résolution"},
	} {
		issues, s := fiche.Conformity(fiche.Parse(c.in))
		if s != c.sugg || (c.issue == "" && len(issues) != 0) || (c.issue != "" && !has(issues, c.issue)) {
			t.Errorf("%s : écarts %v, proposé %q", c.in, issues, s)
		}
	}
}

func has(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestPropose(t *testing.T) {
	mi := fiche.ParseMediaInfo(miWeb1080) // HEVC, français + anglais
	for _, c := range []struct{ name, tmdbTitle, year, want string }{
		// Nom maison (film d'avant la stack *arr) : tout vient de TMDB et du MediaInfo.
		{"Inception (2010)", "Inception", "2010", "Inception.2010.MULTi.1080p.[SOURCE].H265-[TEAM]"},
		{"le fabuleux destin d'amelie poulain", "Amélie", "2001", "Amelie.2001.MULTi.1080p.[SOURCE].H265-[TEAM]"},
		// Nom de scène incomplet : ce qu'il dit est gardé.
		{"Film.2020.1080p.BluRay.x264-GRP", "The Film", "2020", "The.Film.2020.MULTi.1080p.BluRay.x264-GRP"},
		{"Fast & Furious 2009", "Fast & Furious", "2009", "Fast.and.Furious.2009.MULTi.1080p.[SOURCE].H265-[TEAM]"},
	} {
		got := fiche.Propose(fiche.Parse(c.name), mi, c.tmdbTitle, c.year)
		if got != c.want {
			t.Errorf("%s : %q, attendu %q", c.name, got, c.want)
		}
	}
	// Dans la fiche : proposé dès qu'il manque quelque chose.
	f := fiche.Build(fiche.Input{Name: "Inception (2010)", Files: []fiche.File{{Rel: "Inception (2010).mkv", Size: 3 << 30}}, MediaInfo: miWeb1080, TMDB: 27205, TMDBTitle: "Inception", TMDBYear: "2010", Rules: fake.Rules().Fiche()})
	if f.Suggested != "Inception.2010.MULTi.1080p.[SOURCE].H265-[TEAM]" {
		t.Fatalf("proposé %q, écarts %v", f.Suggested, f.Conform)
	}
	// Le titre retouché est jugé, pas le nom du fichier.
	f = fiche.Build(fiche.Input{Name: "Inception (2010)", Title: "Inception.2010.MULTi.1080p.BluRay.x265-Moi", Files: []fiche.File{{Rel: "Inception (2010).mkv", Size: 3 << 30}}, MediaInfo: miWeb1080, TMDB: 27205, Rules: fake.Rules().Fiche()})
	if len(f.Conform) != 0 || f.Suggested != "" {
		t.Fatalf("titre conforme jugé non conforme : %v %q", f.Conform, f.Suggested)
	}
}

// Le titre retouché fait foi : type, team interdite et doublon de nom s'y lisent.
func TestBuildTitleRules(t *testing.T) {
	files := []fiche.File{{Rel: "Vieux Film (1999).mkv", Size: 2 << 30}}
	f := fiche.Build(fiche.Input{Name: "Vieux Film (1999)", Title: "Old.Movie.1999.FRENCH.1080p.BluRay.x264-Moi", Files: files, TMDB: 1, Rules: fake.Rules().Fiche()})
	if f.Fields.TypeID != fake.TypeEncode || f.Fields.Name != "Old.Movie.1999.FRENCH.1080p.BluRay.x264-Moi" || f.TMDBQuery != "Vieux Film 1999" {
		t.Fatalf("%+v / %q", f.Fields, f.TMDBQuery)
	}
	if f := fiche.Build(fiche.Input{Name: "Vieux Film (1999)", Title: "Old.Movie.1999.FRENCH.1080p.WEB.H264-BADTEAM", Files: files, TMDB: 1, Rules: fake.Rules().Fiche()}); !has(f.Blockers, "interdite") {
		t.Fatalf("team du titre : %v", f.Blockers)
	}
}

// HDLight : refusé sur le tracker, que la mention soit dans le titre ou seulement
// dans le nom du fichier.
func TestBuildHDLight(t *testing.T) {
	files := []fiche.File{{Rel: "f.mkv", Size: 2 << 30}}
	if f := fiche.Build(fiche.Input{Name: "Film.2020.MULTi.1080p.HDLight.x264-GRP", Files: files, TMDB: 1, Rules: fake.Rules().Fiche()}); !has(f.Blockers, "HDLIGHT refusé") {
		t.Fatalf("nom : %v", f.Blockers)
	}
	if f := fiche.Build(fiche.Input{Name: "Film.2020.MULTi.1080p.HDLight.x264-GRP", Title: "Film.2020.MULTi.1080p.BluRay.x264-GRP", Files: files, TMDB: 1, Rules: fake.Rules().Fiche()}); !has(f.Blockers, "HDLIGHT refusé") {
		t.Fatalf("titre maquillé : %v", f.Blockers)
	}
	if f := fiche.Build(fiche.Input{Name: "Film.2020.MULTi.1080p.BluRay.x264-HDLighter", Files: files, TMDB: 1, Rules: fake.Rules().Fiche()}); has(f.Blockers, "HDLIGHT") {
		t.Fatalf("faux positif : %v", f.Blockers)
	}
}

func TestBuild4KLightMHD(t *testing.T) {
	files := []fiche.File{{Rel: "f.mkv", Size: 2 << 30}}
	for name, tok := range map[string]string{
		"Film.2020.MULTi.2160p.4KLight.HDR.x265-GRP": "4KLIGHT refusé",
		"Film.2020.FRENCH.1080p.mHD.x264-GRP":        "MHD refusé",
		"Film 2020 MULTi 720p mHD x264-GRP":          "MHD refusé",
	} {
		if f := fiche.Build(fiche.Input{Name: name, Files: files, TMDB: 1, Rules: fake.Rules().Fiche()}); !has(f.Blockers, tok) {
			t.Errorf("%s : %v", name, f.Blockers)
		}
	}
}
