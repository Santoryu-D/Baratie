// Package config garde les réglages de Baratie sur la machine où il tourne.
//
// Dossier : <configuration de l'utilisateur>/Baratie (config.json en 0600,
// cache des empreintes, journal, Garde-manger). Rien de tout ça n'est envoyé
// ailleurs.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"baratie/internal/pathmap"
)

// Config regroupe les réglages.
type Config struct {
	// Le tracker UNIT3D : adresse et jeton API. Le jeton n'est jamais renvoyé
	// à la page.
	TrackerURL string `json:"trackerUrl,omitempty"`
	TrackerKey string `json:"trackerKey,omitempty"`
	// TMDB (clé v3 ou jeton v4) et imgbb (captures d'écran).
	TMDBKey  string `json:"tmdbKey,omitempty"`
	ImgBBKey string `json:"imgbbKey,omitempty"`
	// SeedDir : dossier où Baratie crée les liens physiques des releases
	// publiées, au nom de la fiche. Vide = BARATIE_SEED_DIR ; vide aussi =
	// seed sur les fichiers d'origine.
	SeedDir string `json:"seedDir,omitempty"`
	// Garde-manger : bibliothèques scannées, Radarr facultatif (lecture
	// seule), rythme des recherches sur le tracker (requêtes par minute).
	PantryLibs  []string `json:"pantryLibs,omitempty"`
	RadarrURL   string   `json:"radarrUrl,omitempty"`
	RadarrKey   string   `json:"radarrKey,omitempty"`
	TrackerRate int      `json:"trackerRate,omitempty"`

	// Clients torrent, dans l'ordre choisi.
	Clients []ClientConfig `json:"clients,omitempty"`

	// Catégorie et étiquette posées sur les torrents ajoutés par Baratie.
	Category string `json:"category"`
	Tag      string `json:"tag"`

	// Fichiers et dossiers écartés des releases (motifs comparés au nom seul).
	// nil = motifs par défaut ; liste vide = rien d'écarté (hors fichiers cachés).
	Exclude []string `json:"exclude,omitempty"`

	// Dernier dossier ouvert dans le sélecteur.
	LastDir string `json:"lastDir,omitempty"`

	// Thème de la page : light (défaut) ou dark.
	Theme string `json:"theme,omitempty"`
}

// MaxClients : clients torrent au plus.
const MaxClients = 8

// ClientConfig : un client torrent et sa façon de voir les dossiers.
type ClientConfig struct {
	// ID : identifiant stable, jamais montré.
	ID string `json:"id"`
	// Name : le nom choisi (« Seedbox », « NAS »).
	Name string `json:"name,omitempty"`
	// Kind : qbittorrent (défaut), transmission, deluge, rtorrent.
	Kind     string `json:"kind,omitempty"`
	URL      string `json:"url"`
	User     string `json:"user,omitempty"`
	Pass     string `json:"pass,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
	// PathMap : où Baratie voit les dossiers de CE client.
	PathMap []pathmap.Rule `json:"pathMap,omitempty"`
}

// NewClientID tire l'identifiant d'un nouveau client.
func NewClientID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Client renvoie le client d'identifiant id (nil s'il n'existe pas).
func (c *Config) Client(id string) *ClientConfig {
	for i := range c.Clients {
		if c.Clients[i].ID == id {
			return &c.Clients[i]
		}
	}
	return nil
}

// fixIDs donne un identifiant unique à chaque client.
func (c *Config) fixIDs() {
	seen := map[string]bool{}
	for i := range c.Clients {
		id := c.Clients[i].ID
		for n := i + 1; id == "" || seen[id]; n++ {
			id = "c" + strconv.Itoa(n)
		}
		c.Clients[i].ID = id
		seen[id] = true
	}
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
	return &Config{Tag: "Baratie"}
}

var mu sync.Mutex

// Dir renvoie le dossier de Baratie (créé au besoin, lisible par le seul
// utilisateur). BARATIE_CONFIG le choisit.
func Dir() (string, error) {
	d := strings.TrimSpace(os.Getenv("BARATIE_CONFIG"))
	if d == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(base, "Baratie")
	}
	return d, os.MkdirAll(d, 0o700)
}

// Path renvoie le chemin d'un fichier du dossier de Baratie.
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
	return c, Parse(b, c)
}

// Parse lit un config.json sur c (déjà rempli des réglages par défaut).
func Parse(b []byte, c *Config) error {
	if err := json.Unmarshal(b, c); err != nil {
		return err
	}
	c.fixIDs()
	return nil
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

// WriteFileAtomic écrit un fichier du dossier de Baratie (0600) sans jamais
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
