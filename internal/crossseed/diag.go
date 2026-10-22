package crossseed

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Diag : la fiche de diagnostic d'une ligne de l'analyse, à copier pour le
// staff quand une release n'est pas classée comme attendu (Ori de Nayr,
// 22/10/26 : on devinait à coups de captures). Ni info-hash ni chemin sur le
// disque, ni rien qui dise d'où vient la copie : le nom de la release, ses
// fichiers et leurs tailles, ce que D3X en a conclu.
func (e *Engine) Diag(ctx context.Context, key, version string) (string, bool) {
	e.mu.Lock()
	r := e.rows[key]
	if r == nil {
		e.mu.Unlock()
		return "", false
	}
	row := *r
	row.Status = e.statusOf(r)
	var sig *Sig
	if r.sig != nil {
		s := *r.sig
		sig = &s
	}
	scan := e.scan
	e.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "Diagnostic D3X %s\n", version)
	fmt.Fprintf(&b, "Release : %s\n", row.Name)
	state := statusLabel[row.Status]
	if state == "" {
		state = row.Status
	}
	if row.Detail != "" {
		state += " (" + row.Detail + ")"
	}
	fmt.Fprintf(&b, "État : %s\n", state)
	fmt.Fprintf(&b, "Client : %s, %d copie(s)\n", row.ClientName, max(row.Copies, 1))
	if m := row.Match; m != nil {
		fmt.Fprintf(&b, "Fiche V3X retenue : %s (%s)\n", m.Name, m.Kind)
	}
	if sig != nil {
		fmt.Fprintf(&b, "Fichiers : %d, %d octets (%d sans bourrage)\n", sig.Files, sig.Total, sig.Real)
		if sig.Big > 0 {
			fmt.Fprintf(&b, "Plus gros : %s, %d octets\n", sig.BigName, sig.Big)
		}
		fmt.Fprintf(&b, "Empreintes : contenu %s, tailles %s\n", short(sig.ContentHash), short(sig.SizeHash))
	}

	// Liste des fichiers relue dans le client (30 au plus, les plus gros).
	if env, err := e.clients(); err == nil && row.hash != "" {
		for _, t := range env.Targets {
			if t.ID != row.Client {
				continue
			}
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			files, _, err := t.Client.Files(c, row.hash)
			cancel()
			if err != nil {
				fmt.Fprintf(&b, "Liste des fichiers illisible : %s\n", err.Error())
				break
			}
			sort.Slice(files, func(i, j int) bool { return files[i].Size > files[j].Size })
			b.WriteString("Fichiers dans le client :\n")
			for i, f := range files {
				if i == 30 {
					fmt.Fprintf(&b, "  … et %d autre(s)\n", len(files)-30)
					break
				}
				fmt.Fprintf(&b, "  %s : %d octets\n", f.Path, f.Size)
			}
			break
		}
	}
	if !scan.At.IsZero() {
		fmt.Fprintf(&b, "Analyse du %s (UTC)\n", scan.At.UTC().Format("02/01/2006 15:04"))
	}
	if scan.Warn != "" {
		fmt.Fprintf(&b, "Avertissement : %s\n", scan.Warn)
	}
	return b.String(), true
}

// statusLabel : l'état tel que la page l'affiche.
var statusLabel = map[string]string{
	StAdd: "À ajouter", StRenamed: "Noms différents", StInClient: "Déjà dans le client", StOnV3X: "Seedé pour V3X",
	StNone: "Pas sur V3X", StSameVideo: "Même vidéo ou même fichier sur V3X", StGone: "Retirée de V3X",
	StPartial: "Téléchargé en partie", StError: "Illisible", StMissing: "Absente du disque",
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
