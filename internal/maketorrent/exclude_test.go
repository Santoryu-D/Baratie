package maketorrent

import "testing"

// Dossiers système écartés d'office, même avec une liste d'exclusions vide
// (corbeille Windows illisible à la racine d'un disque).
func TestSystemDirsExcluded(t *testing.T) {
	for _, n := range []string{"$RECYCLE.BIN", "System Volume Information", "@eaDir", "#recycle", "lost+found"} {
		if !Excluded(n, nil) {
			t.Errorf("%q devrait être écarté", n)
		}
	}
	for _, n := range []string{"Films", "Recycle Bin Movie (2020)", "Saison 1"} {
		if Excluded(n, nil) {
			t.Errorf("%q ne devrait pas être écarté", n)
		}
	}
}
