package crossseed

import "testing"

// Médiathèque sur un chemin réseau Windows (odeuxcool, 22/10/26) : le dossier
// garde ses deux « \ » de tête et le fichier reste un nom relatif.
func TestClientPathsUNC(t *testing.T) {
	p := `\\NAS\Media Pool 2\Films\Le Pari (1997)\Le Pari.mkv`
	save := clientCommonDir([]string{p})
	if want := `\\NAS\Media Pool 2\Films\Le Pari (1997)`; save != want {
		t.Fatalf("dossier = %q, voulu %q", save, want)
	}
	if rel := clientRel(save, p); rel != "Le Pari.mkv" {
		t.Fatalf("nom relatif = %q", rel)
	}
	two := []string{`\\NAS\Media\Série\S01\e01.mkv`, `\\NAS\Media\Série\S02\e01.mkv`}
	if d := clientCommonDir(two); d != `\\NAS\Media\Série` {
		t.Fatalf("dossier commun = %q", d)
	}
	folder := commonDir([]libPair{{local: LibFile{Path: p}}})
	if folder != `\\NAS\Media Pool 2\Films\Le Pari (1997)` {
		t.Fatalf("dossier affiché = %q", folder)
	}
	// Chemins ordinaires inchangés.
	for _, c := range [][2]string{
		{`Z:\Films\Le Pari\Le Pari.mkv`, `Z:\Films\Le Pari`},
		{`/data/films/Le Pari/Le Pari.mkv`, `/data/films/Le Pari`},
	} {
		if d := clientCommonDir([]string{c[0]}); d != c[1] {
			t.Fatalf("%s : dossier = %q, voulu %q", c[0], d, c[1])
		}
	}
	// Deux partages différents : aucun parent commun, et pas de boucle.
	if d := clientCommonDir([]string{`\\A\x\f.mkv`, `\\B\y\g.mkv`}); d != "" && d != `\\` && d != `\` {
		t.Fatalf("partages différents : %q", d)
	}
}
