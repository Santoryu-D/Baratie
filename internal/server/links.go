package server

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/V3x0rV3x/D3X/internal/links"
	"github.com/V3x0rV3x/D3X/internal/pathmap"
	"github.com/V3x0rV3x/D3X/internal/source"
)

// linkFiles crée (ou retire) les liens physiques d'un ajout. Les chemins
// arrivent tels que le client les voit (rules : sa correspondance de
// dossiers) : D3X les fait sur ce PC quand il voit le dossier des liens (même
// machine, ou partage avec une correspondance de dossiers), sinon sur la
// seedbox par son agent.
func (s *Server) linkFiles(ctx context.Context, rules []pathmap.Rule, base string, pairs []links.Pair, remove bool) error {
	s.mu.Lock()
	ssh := s.ssh
	symbolic := s.cfg.Links.Symbolic
	s.mu.Unlock()
	if symbolic {
		// Le lien contient le chemin tel que le client le voit : c'est lui
		// qui le suit pour seeder.
		pairs = append([]links.Pair(nil), pairs...)
		for i := range pairs {
			pairs[i].Target = pairs[i].Src
		}
	}
	localBase := pathmap.FromClient(base, rules)
	if st, err := os.Stat(localBase); err == nil && st.IsDir() {
		lp := make([]links.Pair, len(pairs))
		for i, p := range pairs {
			lp[i] = links.Pair{Src: pathmap.FromClient(p.Src, rules), Dst: pathmap.FromClient(p.Dst, rules), Size: p.Size, Target: p.Target}
		}
		if remove {
			return withPermHint(links.Remove(localBase, lp))
		}
		return withPermHint(links.Make(localBase, lp))
	}
	if ssh != nil && strings.HasPrefix(base, "/") {
		return source.Remote{C: ssh}.Links(ctx, base, pairs, remove)
	}
	return errors.New("D3X ne voit pas le dossier des liens « " + base + " » : indique où il le voit (Réglages ▸ Dossiers du client), ou connecte ta seedbox")
}
