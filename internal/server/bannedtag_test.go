package server

import (
	"testing"

	"github.com/V3x0rV3x/D3X/internal/v3x"
)

func TestBannedTagOf(t *testing.T) {
	list := []v3x.TeamPolicy{
		{Name: "Dread-Team", Policy: "banned"},
		{Name: "EXTREME", Policy: "banned"},
		{Name: "TOXIC", Policy: "banned"},
	}
	for _, tc := range []struct {
		name, want string
	}{
		{"Helter.Skelter.2012.VOSTFR.1080p.BluRay.x264-DreadTeam", "Dread-Team"},
		{"Film.2020.1080p-Dread-Team.mkv", "Dread-Team"},
		{"The.Humanity.Bureau.2017.MULTi.1080p.BluRay.x264.AC3-EXTREME", "EXTREME"},
		{"Jackass.Extreme", ""},
		{"Film.2020.1080p.WEB-NOTOXIC", ""},
		{"Film [TOXIC]", "TOXIC"},
		{"Film - TOXIC (Titre original)", "TOXIC"},
	} {
		got := ""
		if b := bannedTagOf(tc.name, list); b != nil {
			got = b.Name
		}
		if got != tc.want {
			t.Errorf("%s : %q, attendu %q", tc.name, got, tc.want)
		}
	}
}
