package links

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakeAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "copie", "Film.2020.mkv")
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	if err := os.WriteFile(src, []byte("donnees"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "liens")
	pairs := []Pair{{Src: src, Dst: filepath.Join(base, "Film 2020", "Film 2020.mkv"), Size: 7}}

	if err := Make(base, pairs); err == nil {
		t.Fatal("dossier des liens absent : refus attendu")
	}
	_ = os.Mkdir(base, 0o755)
	if err := Make(base, []Pair{{Src: src, Dst: filepath.Join(dir, "ailleurs.mkv")}}); err == nil {
		t.Fatal("hors du dossier des liens : refus attendu")
	}
	if err := Make(base, []Pair{{Src: src, Dst: filepath.Join(base, "x.mkv"), Size: 99}}); err == nil {
		t.Fatal("mauvaise taille : refus attendu")
	}
	if err := Make(base, pairs); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(pairs[0].Dst)
	if !os.SameFile(a, b) {
		t.Fatal("le lien n'est pas la même donnée")
	}
	// Jamais rien d'écrasé.
	if err := Make(base, pairs); err == nil {
		t.Fatal("lien déjà là : refus attendu")
	}
	// Un fichier qui n'est pas un lien vers la source n'est pas effacé.
	other := filepath.Join(base, "Film 2020", "autre.nfo")
	_ = os.WriteFile(other, []byte("x"), 0o644)
	if err := Remove(base, []Pair{{Src: src, Dst: other}}); err == nil {
		t.Fatal("fichier étranger : refus attendu")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("le fichier étranger a été effacé")
	}
	_ = os.Remove(other)
	if err := Remove(base, pairs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("la source a disparu")
	}
	if _, err := os.Stat(filepath.Join(base, "Film 2020")); !os.IsNotExist(err) {
		t.Fatal("le dossier vide est resté")
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatal("le dossier des liens a été retiré")
	}
}

// Au premier échec, rien ne reste.
func TestMakeAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "liens")
	_ = os.Mkdir(base, 0o755)
	a := filepath.Join(dir, "a.mkv")
	_ = os.WriteFile(a, []byte("a"), 0o644)
	pairs := []Pair{
		{Src: a, Dst: filepath.Join(base, "R", "a.mkv")},
		{Src: filepath.Join(dir, "absent.mkv"), Dst: filepath.Join(base, "R", "b.mkv")},
	}
	if err := Make(base, pairs); err == nil {
		t.Fatal("source absente : refus attendu")
	}
	if _, err := os.Stat(filepath.Join(base, "R")); !os.IsNotExist(err) {
		t.Fatal("un lien est resté après l'échec")
	}
}
