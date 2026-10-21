package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/crossseed"
)

// Une ligne qui fusionne plusieurs copies (exodius19, 21/10/26) : Publier et
// le Lot prennent une copie que D3X sait lire, l'original avant le dossier de
// liens d'un outil de cross-seed.
func TestResolveCopies(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	cfg := config.Defaults()
	cfg.CheckUpdates = false
	s := NewWith("dev", cfg, "jeton-de-test-0123456789abcdef", 8733, Options{})

	orig := filepath.Join(tmp, "downloads", "Film.mkv")
	links := filepath.Join(tmp, "cross-seed-links", "SITE", "Film.mkv")
	for _, p := range []string{orig, links} {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	missing := filepath.Join(tmp, "cross-seed-links", "AUTRE", "Film.mkv")
	ctx := context.Background()

	if lp := s.resolveCopies(ctx, []crossseed.RowCopy{{Path: missing}, {Path: orig}}); lp.Path != orig {
		t.Fatalf("copie illisible d'abord : %+v", lp)
	}
	if lp := s.resolveCopies(ctx, []crossseed.RowCopy{{Path: links}, {Path: orig}}); lp.Path != orig {
		t.Fatalf("l'original passe avant le dossier de liens : %+v", lp)
	}
	if lp := s.resolveCopies(ctx, []crossseed.RowCopy{{Path: missing}}); lp.Path != "" || lp.Error == "" {
		t.Fatalf("aucune copie lisible : %+v", lp)
	}
}
