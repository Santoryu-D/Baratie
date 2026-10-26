package links

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// Liens symboliques (copie en lecture seule) : le lien contient
// le chemin donné (celui que voit le client), se lit comme le fichier, et un
// retrait ne touche jamais la copie ni un lien qui vise autre chose.
func TestSymbolicLinks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nfs", "Saw VII.mkv")
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	if err := os.WriteFile(src, []byte("donnees"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "liens")
	_ = os.MkdirAll(base, 0o755)
	dst := filepath.Join(base, "Saw.VII.2010", "Saw.VII.2010.mkv")
	pairs := []Pair{{Src: src, Dst: dst, Size: 7, Target: src}}
	if err := Make(base, pairs); err != nil {
		if strings.Contains(err.Error(), "mode développeur") {
			t.Skip("liens symboliques refusés par ce Windows : " + err.Error())
		}
		t.Fatal(err)
	}
	if got, err := os.Readlink(dst); err != nil || got != src {
		t.Fatalf("contenu du lien = %q, %v", got, err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "donnees" {
		t.Fatalf("lecture par le lien : %q, %v", b, err)
	}
	other := []Pair{{Src: src, Dst: dst, Size: 7, Target: filepath.Join(dir, "ailleurs.mkv")}}
	if err := Remove(base, other); err == nil {
		t.Fatal("un lien qui vise autre chose ne doit pas être retiré")
	}
	if err := Remove(base, pairs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("lien encore là")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("la copie a été touchée")
	}
	if _, err := os.Stat(filepath.Dir(dst)); !os.IsNotExist(err) {
		t.Fatal("dossier vide laissé")
	}
}

// Un refus d'écriture reste reconnaissable après l'explication : la page
// peut alors dire quoi faire (droits du dossier sous Docker).
func TestExplainKeepsPermission(t *testing.T) {
	for _, cause := range []error{fs.ErrPermission, syscall.EPERM} {
		err := explain(&os.LinkError{Op: "link", Old: "/data/Film.mkv", New: "/data/seed/Film.mkv", Err: cause}, false)
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%v : refus perdu (%s)", cause, err)
		}
	}
	if err := explain(errors.New("invalid cross-device link"), false); !strings.Contains(err.Error(), "même disque") {
		t.Errorf("autre disque : %s", err)
	}
}
