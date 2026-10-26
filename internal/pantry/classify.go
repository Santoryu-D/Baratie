package pantry

import (
	"path/filepath"
	"strings"

	"baratie/internal/fiche"
	"baratie/internal/tracker"
)

// Classes d'un film.
const (
	ClassAbsent    = "absent"     // le tracker n'a aucune version
	ClassComplete  = "complement" // nouvelle combinaison (source, résolution, codec)
	ClassMaybe     = "maybe"      // doublon possible : à juger (info manquante, version plus lourde)
	ClassCovered   = "covered"    // doublon selon les règles du tracker
	ClassIdentical = "identical"  // tes fichiers exacts sont déjà là
	ClassExcluded  = "excluded"   // team interdite, série, disque complet
	ClassUnknown   = "unknown"    // film à identifier
	ClassEvaluate  = "evaluate"   // le tracker a le film, ta version est à mesurer
	ClassPending   = "pending"    // identifié, pas encore cherché sur le tracker
)

// Classify classe un film d'après ce que dit son nom et ce que le tracker a.
func Classify(it *Item, rules fiche.Rules) (string, string) {
	rel := fiche.Parse(it.Name)
	g := strings.ToLower(rel.Group)
	for tag, why := range rules.Banned {
		if g != "" && g == strings.ToLower(tag) {
			return ClassExcluded, "team interdite : " + tag + " (" + why + ")"
		}
	}
	if rel.TV {
		return ClassExcluded, "série"
	}
	if tok, why := fiche.BannedToken(it.Name, rules); tok != "" {
		return ClassExcluded, tok + " (" + why + ")"
	}
	if tok, why := fiche.BannedToken(filepath.Base(it.Path), rules); tok != "" {
		return ClassExcluded, tok + " (" + why + ")"
	}
	if strings.EqualFold(strings.TrimPrefix(strings.ToLower(it.Ext()), "."), "iso") {
		return ClassExcluded, "image disque (Full Disc : BDInfo exigé)"
	}
	// Règles d'upload et d'encodage, sur le fichier lu par ffprobe (0.6.0).
	if it.Probe != nil {
		f := fiche.Build(fiche.Input{Name: it.Name, Files: []fiche.File{{Rel: filepath.Base(it.Path), Size: it.Size}}, MI: it.Probe, TMDB: it.TMDB, OrigLang: it.OrigLang, Animation: it.Animation, Rules: rules})
		if n := len(f.RuleBlockers); n > 0 {
			why := f.RuleBlockers[0]
			if n > 1 {
				why += " (et " + plural(n-1, "autre règle", "autres règles") + ")"
			}
			return ClassExcluded, why
		}
	}
	if it.TMDB == 0 {
		return ClassUnknown, "film à identifier sur TMDB"
	}
	if it.CheckedAt.IsZero() {
		return ClassPending, "pas encore cherché sur " + rules.Tracker
	}
	if len(it.Versions) == 0 {
		return ClassAbsent, rules.Tracker + " n'a aucune version de ce film"
	}
	for _, v := range it.Versions {
		if identical(v, it.Size) {
			if it.Published != "" && v.Name == it.Published {
				return ClassIdentical, "publiée par toi sur " + rules.Tracker + " : " + v.Name
			}
			return ClassIdentical, "ta release est déjà sur " + rules.Tracker + " : " + v.Name
		}
	}
	res, lang := resOf(rel), langOf(rel)
	var mi fiche.MediaInfo
	if it.Probe != nil {
		mi = *it.Probe
		if r := strings.ToLower(mi.Resolution()); r != "" {
			res = r
		}
		if lang == "" {
			switch {
			case mi.HasFrenchAudio():
				lang = "fr"
			case mi.HasFrenchSubs():
				lang = "vost"
			}
		}
	}
	if res == "" || lang == "" {
		why := rules.Tracker + " a ce film ; ta résolution ou ta langue n'est pas dans le nom"
		if it.Probe == nil && it.ProbeErr != "" {
			why += " (fichier illisible par ffprobe : " + it.ProbeErr + ")"
		} else if it.Probe == nil {
			why += " (lecture du fichier en attente)"
		} else {
			why += " ni lisible dans le fichier (mesurée à la fiche)"
		}
		return ClassEvaluate, why
	}
	// Politique des doublons : même genre, source, résolution et codec
	// (d'après le nom ; la fiche affine avec le MediaInfo).
	kind := fiche.KindOf(rel)
	if kind == "" && mi.Library != "" {
		kind = fiche.TypeEncode
	}
	mine := fiche.KeyOf(rel, kind, mi, res)
	maybe := ""
	for _, v := range it.Versions {
		if versionLang(v) != lang {
			continue
		}
		switch d, why := fiche.Compare(mine, it.Size, v); d {
		case fiche.DupeSame:
			return ClassCovered, why + " : " + v.Name
		case fiche.DupeMaybe:
			if maybe == "" {
				maybe = why + " : " + v.Name
			}
		}
	}
	if maybe != "" {
		return ClassMaybe, "doublon possible, " + maybe
	}
	return ClassComplete, rules.Tracker + " n'a pas encore de version " + mine.String() + " " + langLabel(lang)
}

// identical : la version contient un fichier de la taille exacte du film
// (au-delà de 100 Mio, deux fichiers différents n'ont pas la même taille).
func identical(v tracker.Version, size int64) bool {
	if size < 100<<20 {
		return false
	}
	for _, s := range v.Files {
		if s == size {
			return true
		}
	}
	return len(v.Files) == 0 && v.Size == size
}

func resOf(r fiche.Release) string { return strings.ToLower(r.Res) }

// langOf : « vost » (VOSTFR, SUBFRENCH) ou « fr » (MULTi, FRENCH, TRUEFRENCH).
func langOf(r fiche.Release) string {
	switch r.Lang {
	case "VOSTFR", "SUBFRENCH":
		return "vost"
	case "MULTi", "FRENCH", "TRUEFRENCH":
		return "fr"
	}
	return ""
}

func versionLang(v tracker.Version) string {
	if strings.Contains(strings.ToUpper(v.Category), "VOSTFR") {
		return "vost"
	}
	return "fr"
}

func langLabel(l string) string {
	if l == "vost" {
		return "VOSTFR"
	}
	return "en français"
}
