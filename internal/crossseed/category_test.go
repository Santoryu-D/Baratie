package crossseed

import "testing"

func TestCategoryFor(t *testing.T) {
	cases := []struct{ tpl, src, want string }{
		{"v3x", "tv", "v3x"},                            // pas de modèle : inchangé
		{"{cat}.cross", "tv", "tv.cross"},               // rangé à côté de l'original
		{"{cat}.cross", "movies.cross", "movies.cross"}, // déjà rangé : pas de .cross.cross
		{"{cat}.cross", "", "cross"},                    // sans catégorie d'origine
		{"cross/{cat}", "anime", "cross/anime"},
		{"cross/{cat}", "cross/anime", "cross/anime"},
		{"{cat}", "tv", "tv"},
		{"{cat}", "", ""},
		{"{cat}.cross", ".cross", ".cross.cross"}, // pas de nom avant le suffixe : on ajoute
	}
	for _, c := range cases {
		if got := CategoryFor(c.tpl, c.src); got != c.want {
			t.Errorf("CategoryFor(%q, %q) = %q, attendu %q", c.tpl, c.src, got, c.want)
		}
	}
}
