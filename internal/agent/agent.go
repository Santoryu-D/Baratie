// Package agent est D3X lancé sur une seedbox (« d3x agent … ») : il lit les
// fichiers là où ils sont, avec exactement le même code qu'en local, et
// renvoie le résultat sur sa sortie. Chaque demande arrive en un seul
// argument (JSON en base64) : aucun nom de fichier ne passe par le shell.
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/V3x0rV3x/D3X/internal/audio"
	"github.com/V3x0rV3x/D3X/internal/links"
	"github.com/V3x0rV3x/D3X/internal/maketorrent"
	"github.com/V3x0rV3x/D3X/internal/releases"
)

// Request est une demande à l'agent.
type Request struct {
	Path        string   `json:"path,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	Exclude     []string `json:"exclude,omitempty"`
	PieceLength int64    `json:"pieceLength,omitempty"`
	CreatedBy   string   `json:"createdBy,omitempty"`
	Offset      int64    `json:"offset,omitempty"`
	Size        int64    `json:"size,omitempty"`
	// Liens physiques (commandes link et unlink) : tout sous Base.
	Base  string       `json:"base,omitempty"`
	Links []links.Pair `json:"links,omitempty"`
}

// Encode prépare l'argument d'une demande.
func Encode(r Request) string {
	b, _ := json.Marshal(r)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decode(arg string) (Request, error) {
	var r Request
	b, err := base64.RawURLEncoding.DecodeString(arg)
	if err != nil {
		return r, errors.New("demande illisible")
	}
	return r, json.Unmarshal(b, &r)
}

// Run exécute une commande de l'agent : version, ls, list, entries, scan,
// audio, hash, read, link, unlink.
func Run(version string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("commande de l'agent manquante")
	}
	cmd := args[0]
	if cmd == "version" {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}
	if len(args) < 2 {
		return errors.New("demande manquante")
	}
	req, err := decode(args[1])
	if err != nil {
		return err
	}
	switch cmd {
	case "ls":
		path := req.Path
		if path == "" || path == "~" {
			if home, err := os.UserHomeDir(); err == nil {
				path = home
			}
		}
		l, err := releases.Browse(path, req.Exclude)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(l)
	case "list":
		l, err := releases.List(req.Path, req.Exclude)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(l)
	case "entries":
		return json.NewEncoder(stdout).Encode(releases.Entries(req.Paths, req.Exclude))
	case "scan":
		p, err := maketorrent.Scan(req.Path, req.Exclude)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(p)
	case "audio":
		p, err := maketorrent.Scan(req.Path, req.Exclude)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(audio.FromPlan(p))
	case "hash":
		p, err := maketorrent.Scan(req.Path, req.Exclude)
		if err != nil {
			return err
		}
		if req.PieceLength > 0 {
			p.PieceLength = req.PieceLength
		}
		// Coupure de la connexion SSH : le hachage s'arrête aussi.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		last := time.Time{}
		raw, cached, err := p.Hash(ctx, req.CreatedBy, func(done, total int64) {
			if time.Since(last) >= 250*time.Millisecond || done == total {
				last = time.Now()
				fmt.Fprintf(stderr, "P %d %d\n", done, total)
			}
		})
		if err != nil {
			return err
		}
		if cached {
			fmt.Fprintln(stderr, "C")
		}
		_, err = stdout.Write(raw)
		return err
	case "link", "unlink":
		var err error
		if cmd == "link" {
			err = links.Make(req.Base, req.Links)
		} else {
			err = links.Remove(req.Base, req.Links)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(true)
	case "read":
		f, err := os.Open(req.Path)
		if err != nil {
			return err
		}
		defer f.Close()
		if req.Size > 32<<20 {
			req.Size = 32 << 20
		}
		_, err = io.Copy(stdout, io.NewSectionReader(f, req.Offset, req.Size))
		return err
	}
	return fmt.Errorf("commande inconnue : %s", strings.TrimSpace(cmd))
}
