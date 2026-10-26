package unit3d_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"baratie/internal/bencode"
	"baratie/internal/torrent"
	"baratie/internal/tracker"
	"baratie/internal/unit3d"
	"baratie/internal/unit3d/fake"
)

func localTorrent(t *testing.T) []byte {
	raw, err := bencode.Encode(bencode.Dict{
		"announce": []byte("https://tracker.exemple/announce"),
		"info": bencode.Dict{
			"name": []byte("Film.2024.MULTi.1080p.WEB.H264-GRP.mkv"), "length": int64(1 << 20),
			"piece length": int64(1 << 18), "pieces": []byte(strings.Repeat("a", 80)),
			"private": int64(1), "source": []byte("LOCAL"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCheck(t *testing.T) {
	s := fake.New()
	defer s.Close()
	a, err := unit3d.New(s.URL, fake.Token, "test", fake.Rules()).Check(context.Background())
	if err != nil || a.Username != "Membre" || a.Group != "Uploader" || a.Ratio == nil || *a.Ratio != 10 {
		t.Fatalf("%+v %v", a, err)
	}
	_, err = unit3d.New(s.URL, "mauvais", "test", fake.Rules()).Check(context.Background())
	var ae *unit3d.APIError
	if !errors.As(err, &ae) || ae.Status != 401 || strings.Contains(err.Error(), "mauvais") {
		t.Fatalf("token refusé : %v", err)
	}
}

func TestExisting(t *testing.T) {
	s := fake.New(
		&fake.Torrent{ID: 429390, Name: "Inception.2010.MULTi.1080p.WEB.H265-CHiLL", Size: 3 << 30, Category: 1, Type: "WEB", Resolution: "1080p", TMDB: 27205, Seeders: 12},
		&fake.Torrent{ID: 2, Name: "Autre.2020.MULTi.1080p.WEB.H264-X", Category: 1, TMDB: 1},
	)
	defer s.Close()
	c := unit3d.New(s.URL, fake.Token, "test", fake.Rules())
	vs, err := c.Existing(context.Background(), tracker.ExistingQuery{TMDB: 27205, Categories: []int{1, 6}})
	if err != nil || len(vs) != 1 || vs[0].ID != "429390" || vs[0].Type != "WEB" || vs[0].Seeders != 12 || vs[0].URL != s.URL+"/torrents/429390" {
		t.Fatalf("%+v %v", vs, err)
	}
	if _, err := c.Existing(context.Background(), tracker.ExistingQuery{}); err == nil {
		t.Fatal("recherche vide acceptée")
	}
}

func TestUploadAndSeedTorrent(t *testing.T) {
	s := fake.New()
	defer s.Close()
	c := unit3d.New(s.URL, fake.Token, "test", fake.Rules())
	raw := localTorrent(t)
	f := tracker.Fields{Name: "Film.2024.MULTi.1080p.WEB.H264-GRP", Description: "Film.2024.MULTi.1080p.WEB.H264-GRP", MediaInfo: "General", CategoryID: 1, TypeID: 4, ResolutionID: 3, TMDB: 42, IMDB: 1375666}
	up, err := c.Upload(context.Background(), "Film.torrent", raw, []byte("NFO"), f)
	if err != nil {
		t.Fatal(err)
	}
	if up.ID == "" || up.URL != s.URL+"/torrents/"+up.ID || !strings.Contains(up.Download, fake.RSSKey) {
		t.Fatalf("%+v", up)
	}
	form := s.LastForm
	if form["tmdb"] != "42" || form["imdb"] != "1375666" || form["anonymous"] != "0" || form["free"] != "0" || string(s.LastNFO) != "NFO" {
		t.Fatalf("champs : %v", form)
	}
	// UNIT3D exige tous les identifiants : 0 quand il n'y en a pas (pas de TVDB pour un film).
	if form["tvdb"] != "0" || form["igdb"] != "0" || form["mal"] != "0" {
		t.Fatalf("identifiants absents : %v", form)
	}
	site, err := c.SeedTorrent(context.Background(), up)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := torrent.Parse(raw)
	b, err := torrent.Parse(site)
	if err != nil || a.InfoHash == b.InfoHash {
		t.Fatalf("empreinte : locale %s, site %s (%v)", a.InfoHash, b.InfoHash, err)
	}
	// Même nom : refusé par le site, détail lisible.
	_, err = c.Upload(context.Background(), "Film.torrent", raw, nil, f)
	var ue *tracker.UploadError
	if !errors.As(err, &ue) || !strings.Contains(ue.Code, "name : The name has already been taken") {
		t.Fatalf("doublon : %v", err)
	}
	if strings.Contains(err.Error(), fake.RSSKey) {
		t.Fatal("clé RSS dans une erreur")
	}
}

func TestUploadIncomplete(t *testing.T) {
	s := fake.New()
	defer s.Close()
	_, err := unit3d.New(s.URL, fake.Token, "test", fake.Rules()).Upload(context.Background(), "x.torrent", localTorrent(t), nil, tracker.Fields{Name: "x"})
	if err == nil || s.Uploads != 0 {
		t.Fatal("fiche incomplète envoyée")
	}
}
