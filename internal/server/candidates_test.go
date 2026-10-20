package server

import (
	"testing"

	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

func TestMatchRequest(t *testing.T) {
	reqs := []v3x.Request{
		{ID: "a", Title: "Le Comte de Monte-Cristo (2024)", MediaType: "movie", Reward: 1200},
		{ID: "b", Title: "Dune", Year: "2021", MediaType: "movie", Reward: 300},
		{ID: "c", Title: "Shogun", MediaType: "tv", Reward: 500},
		{ID: "d", Title: "Alien", Year: "1979", MediaType: "movie"},
		{ID: "e", Title: "Elsbeth (2024) - Saison 3", Year: "2024", MediaType: "tv", Reward: 400},
		{ID: "f", Title: "Doctor Who \u2014 S00E210", MediaType: "tv", Reward: 1500},
	}
	cases := []struct {
		name string
		want []string
	}{
		{"Le.Comte.de.Monte-Cristo.2024.MULTi.1080p.WEB.H264-GRP", []string{"a"}},
		{"Le Comte de Monte Cristo (2024) [1080p]", []string{"a"}},
		{"Dune.2021.2160p.UHD.BluRay.x265-GRP", []string{"b"}},
		{"Dune.Part.Two.2024.1080p.WEB-DL", nil}, // autre film
		{"Dune.1984.1080p.BluRay", nil},          // autre année
		{"Shogun.2024.S01.MULTi.1080p.WEB", []string{"c"}},
		{"Shogun.1980.1080p.BluRay", nil}, // un film, la demande est une série
		{"Alien.Romulus.2024.1080p", nil},
		{"Alien.1979.REMASTERED.1080p.BluRay", []string{"d"}},
		{"Elsbeth.S03.MULTi.1080p.WEB.H264-GRP", []string{"e"}},
		{"Elsbeth.2024.S03E04.1080p.WEB", []string{"e"}},
		{"Elsbeth.S01.MULTi.1080p.WEB.H264-GRP", nil}, // autre saison
		{"Doctor.Who.S00E210.1080p.WEB", []string{"f"}},
		{"Doctor.Who.S00E211.1080p.WEB", nil}, // autre épisode
	}
	for _, c := range cases {
		hits := matchRequest(c.name, reqs)
		var got []string
		for _, h := range hits {
			got = append(got, h.ID)
		}
		if len(got) != len(c.want) || (len(got) > 0 && got[0] != c.want[0]) {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestCleanRules(t *testing.T) {
	out, err := cleanRules([]pathmap.Rule{{From: " Z:\\Films ", To: "/data/films"}, {}, {From: "z:\\films", To: "/data/films"}})
	if err != nil || len(out) != 1 || out[0].From != "Z:\\Films" {
		t.Fatalf("règles : %v %v", out, err)
	}
	if _, err := cleanRules([]pathmap.Rule{{From: "Z:\\Films"}}); err == nil {
		t.Fatal("une ligne à moitié remplie doit être refusée")
	}
}
