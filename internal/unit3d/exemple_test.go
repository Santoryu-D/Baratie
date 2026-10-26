package unit3d

import (
	"path/filepath"
	"testing"
)

// Le fichier d'exemple livré à la racine du dépôt doit rester lisible.
func TestExampleRules(t *testing.T) {
	p, _ := filepath.Abs("../../regles.exemple.json")
	t.Setenv("BARATIE_RULES", p)
	r, found, err := LoadRules()
	if err != nil || !found {
		t.Fatalf("regles.exemple.json : %v (trouvé : %v)", err, found)
	}
	if r.Name != "MonTracker" || r.Categories["films"] != 1 || r.Types["webrip"] != 5 || r.Quality == nil || r.Quality.MinKbps["bluray/x264/1080p"] != 8000 {
		t.Fatalf("règles lues : %+v", r)
	}
}
