// Package tracker : ce que Baratie attend d'un tracker où publier.
//
// Baratie publie vers des trackers qui ont une API d'upload (UNIT3D). Chaque
// tracker est un adaptateur qui remplit cette interface ; le reste (scan,
// hachage, MediaInfo, ajout dans le client) ne change pas.
//
// Règle cardinale : un adaptateur n'envoie rien de lui-même. Upload n'est
// appelé que sur un clic du membre, release par release.
package tracker

import (
	"context"
	"fmt"
)

// Tracker : un site où publier.
type Tracker interface {
	// ID : identifiant court et stable (« unit3d »).
	ID() string
	// Name : nom affiché (« MonTracker »).
	Name() string
	// Check vérifie la clé et renvoie le compte.
	Check(ctx context.Context) (*Account, error)
	// Context : catégories, types, résolutions… pour remplir la fiche.
	Context() *PubContext
	// Existing : releases déjà sur le site pour ce contenu (doublons, autres
	// versions).
	Existing(ctx context.Context, q ExistingQuery) ([]Version, error)
	// Upload publie la release : le .torrent, le NFO éventuel et les champs.
	Upload(ctx context.Context, torrentName string, torrent, nfo []byte, f Fields) (*Uploaded, error)
	// SeedTorrent rend le .torrent à seeder (celui du site, à ta clé : le
	// site le réécrit, son empreinte n'est plus celle du .torrent local).
	SeedTorrent(ctx context.Context, up *Uploaded) ([]byte, error)
	// TorrentURL : l'adresse de la fiche sur le site.
	TorrentURL(id string) string
}

// Account : le compte lié à la clé.
type Account struct {
	Username string   `json:"username"`
	Group    string   `json:"group,omitempty"`
	Ratio    *float64 `json:"ratio,omitempty"`
}

// Option : une valeur proposée dans la fiche.
type Option struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// PubContext : de quoi remplir la fiche.
type PubContext struct {
	Tracker     string   `json:"tracker"`
	Name        string   `json:"name"`
	Categories  []Option `json:"categories"`
	Types       []Option `json:"types"`
	Resolutions []Option `json:"resolutions"`
}

// ExistingQuery : ce qu'on cherche sur le site. Un champ vide est ignoré.
type ExistingQuery struct {
	Name       string
	TMDB       int
	IMDB       int
	Categories []int
}

// Version : une release déjà sur le site.
type Version struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Category   string `json:"category,omitempty"`
	Type       string `json:"type,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Seeders    int    `json:"seeders"`
	Leechers   int    `json:"leechers"`
	URL        string `json:"url,omitempty"`
	InfoHash   string `json:"-"`
	TMDB       int    `json:"tmdb,omitempty"`
	// Files : tailles des fichiers (comparées à la release locale).
	Files []int64 `json:"files,omitempty"`
	// Identical : mêmes fichiers, mêmes tailles que la release locale.
	Identical bool `json:"identical,omitempty"`
	// Same : même nom que la release locale (le site la refuserait).
	Same bool `json:"same,omitempty"`
	// Dupe : doublon selon la politique du tracker (« same », « maybe »).
	Dupe    string `json:"dupe,omitempty"`
	DupeWhy string `json:"dupeWhy,omitempty"`
}

// Fields : la fiche telle qu'elle part (noms neutres, l'adaptateur les
// traduit vers son API).
type Fields struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	MediaInfo    string `json:"mediainfo"`
	BDInfo       string `json:"bdinfo,omitempty"`
	CategoryID   int    `json:"categoryId"`
	TypeID       int    `json:"typeId"`
	ResolutionID int    `json:"resolutionId"`
	TMDB         int    `json:"tmdb"`
	IMDB         int    `json:"imdb"`
	TVDB         int    `json:"tvdb,omitempty"`
	MAL          int    `json:"mal,omitempty"`
	Anonymous    bool   `json:"anonymous"`
	Stream       bool   `json:"stream"`
	SD           bool   `json:"sd"`
	Personal     bool   `json:"personal"`
	Keywords     string `json:"keywords,omitempty"`
}

// Uploaded : la réponse du site à une publication.
type Uploaded struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"` // approved, pending
	Download string `json:"-"`                // adresse du .torrent à ta clé : jamais montrée
	URL      string `json:"url,omitempty"`
}

// UploadError garde le détail d'un refus (champ fautif, doublon…).
type UploadError struct {
	Status int
	Code   string
	Body   string
}

func (e *UploadError) Error() string {
	return fmt.Sprintf("publication refusée : %s", e.Code)
}
