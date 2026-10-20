package server

import "testing"

func TestReleaseGroup(t *testing.T) {
	for name, want := range map[string]string{
		"Movie.2020.1080p.WEB-DL.x264-TEAM":            "TEAM",
		"Show.S01.MULTi.1080p.BluRay.x265-QTZ (Title)": "QTZ",
		"Album - Artist (2020) [FLAC]":                 "",
		"Movie.2020.1080p.WEB-DL":                      "",
		"Movie.2020.MULTi.1080p.WEB.x265-BONBON.mkv":   "BONBON",
		"Movie 2020 - 1080p":                           "",
		"Anime.S01.1080p-Tsundere-Raws":                "",
	} {
		if got := releaseGroup(name); got != want {
			t.Errorf("%q : %q, attendu %q", name, got, want)
		}
	}
}
