// Package config garde les réglages de D3X sur la machine du membre.
//
// Dossier : <configuration de l'utilisateur>/D3X (config.json en 0600, cache
// des empreintes, journal). Rien de tout ça n'est envoyé ailleurs.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/V3x0rV3x/D3X/internal/pathmap"
)

// Config regroupe les réglages.
type Config struct {
	V3XBase string `json:"v3xBase,omitempty"` // vide = https://api.v3x.club
	V3XKey  string `json:"v3xKey"`

	// Client torrent : qbittorrent (défaut), transmission, deluge, rtorrent.
	// Les champs gardent leur nom « qbit » pour relire les anciens réglages.
	ClientKind   string `json:"clientKind,omitempty"`
	QbitURL      string `json:"qbitUrl"`
	QbitUser     string `json:"qbitUser"`
	QbitPass     string `json:"qbitPass"`
	QbitInsecure bool   `json:"qbitInsecure,omitempty"`

	// Catégorie et étiquette posées sur les torrents ajoutés par D3X.
	Category string `json:"category"`
	Tag      string `json:"tag"`

	// Vérifier au lancement qu'une nouvelle version existe (GitHub).
	CheckUpdates bool `json:"checkUpdates"`

	// Fichiers et dossiers écartés des releases (motifs comparés au nom seul).
	// nil = motifs par défaut ; liste vide = rien d'écarté (hors fichiers cachés).
	Exclude []string `json:"exclude,omitempty"`

	// Correspondance des chemins entre D3X et le client torrent.
	PathMap []pathmap.Rule `json:"pathMap,omitempty"`

	// Dernier dossier ouvert dans le sélecteur.
	LastDir string `json:"lastDir,omitempty"`

	// Seedbox jointe par SSH (sans secret : le mot de passe n'est jamais écrit).
	SSH *SSH `json:"ssh,omitempty"`

	// Apparence : fond (dark, light, blue) et accent ; vide = comme sur V3X.
	UITheme  string `json:"uiTheme,omitempty"`
	UIAccent string `json:"uiAccent,omitempty"`

	// Surveillance : nouvelle analyse régulière du client, notifications.
	Watch Watch `json:"watch"`

	// Liens physiques : quand renommer dans le client est impossible, la
	// release V3X est rangée dans Links.Dir (vu par le client), ses fichiers
	// étant des liens vers ceux de ta copie.
	Links Links `json:"links"`

	// Médiathèque : dossiers où chercher des releases V3X (films et séries rangés).
	Library []LibFolder `json:"library,omitempty"`
}

// LibFolder : un dossier de la médiathèque, sur ce PC (local) ou sur la
// seedbox (ssh).
type LibFolder struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

// Links : le repli par liens physiques.
type Links struct {
	On  bool   `json:"on"`
	Dir string `json:"dir,omitempty"`
}

// Watch : la surveillance du client.
type Watch struct {
	On      bool `json:"on"`
	Minutes int  `json:"minutes,omitempty"` // entre deux analyses (30 par défaut)
	Notify  bool `json:"notify"`
	// AutoAdd : ajoute seules les releases identiques trouvées depuis la
	// dernière analyse (jamais les « noms différents »).
	AutoAdd bool `json:"autoAdd,omitempty"`
}

// Every : l'intervalle entre deux analyses, borné.
func (w Watch) Every() int {
	switch {
	case w.Minutes <= 0:
		return 30
	case w.Minutes < 15:
		return 15
	case w.Minutes > 24*60:
		return 24 * 60
	}
	return w.Minutes
}

// ExcludeList renvoie les motifs écartés en vigueur.
func (c *Config) ExcludeList() []string {
	if c.Exclude == nil {
		return DefaultExclude
	}
	return c.Exclude
}

// DefaultExclude : ce que les médiathèques déposent à côté d'un film et qui
// n'a rien à faire dans une release.
var DefaultExclude = []string{"*.trickplay", "extrafanart", "extrathumbs", "Thumbs.db", "desktop.ini", "@eaDir", "#recycle", "*.part", "*.!qB", "*.parts"}

// Defaults renvoie une configuration neuve.
func Defaults() *Config {
	return &Config{Tag: "D3X", CheckUpdates: true, Watch: Watch{Notify: true}}
}

var mu sync.Mutex

// Dir renvoie le dossier de D3X (créé au besoin, lisible par le seul membre).
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "D3X")
	return d, os.MkdirAll(d, 0o700)
}

// Path renvoie le chemin d'un fichier du dossier de D3X.
func Path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// Load lit la configuration (réglages par défaut si absente).
func Load() (*Config, error) {
	mu.Lock()
	defer mu.Unlock()
	p, err := Path("config.json")
	if err != nil {
		return nil, err
	}
	c := Defaults()
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	return c, json.Unmarshal(b, c)
}

// Save enregistre la configuration.
func Save(c *Config) error {
	mu.Lock()
	defer mu.Unlock()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic("config.json", b)
}

// WriteFileAtomic écrit un fichier du dossier de D3X (0600) sans jamais
// laisser un fichier à moitié écrit.
func WriteFileAtomic(name string, data []byte) error {
	p, err := Path(name)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// SSH : la seedbox jointe par SSH (sans secret).
type SSH struct {
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
	User    string `json:"user"`
	KeyPath string `json:"keyPath,omitempty"` // vide = clés habituelles de ~/.ssh
	HostKey string `json:"hostKey,omitempty"` // clé d'hôte acceptée (format authorized_keys)
}

// Label : « user@hôte ».
func (c SSH) Label() string { return c.User + "@" + c.Host }
