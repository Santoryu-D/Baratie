package fiche

import "strings"

// Conformity compare le nom à la convention des films :
// TITRE.ANNEE.LANGUE.RESOLUTION.SOURCE.CODEC-TEAM (convention de nommage des trackers FR).
// Rend les écarts et, si un simple réordonnancement suffit, le nom proposé.
// Le nom proposé n'est QUE le titre de la fiche : aucun fichier n'est
// renommé.
func Conformity(r Release) ([]string, string) {
	out := []string{}
	if r.Year == "" {
		out = append(out, "année absente")
	}
	if r.Lang == "" {
		out = append(out, "langue absente (MULTi, FRENCH, TRUEFRENCH, VOSTFR, SUBFRENCH)")
	}
	if r.Res == "" {
		out = append(out, "résolution absente")
	}
	if r.Source == "" {
		out = append(out, "source absente (WEB, BluRay, HDTV…)")
	}
	if r.Codec == "" && r.Source != "DVD" {
		out = append(out, "codec vidéo absent")
	}
	if r.Group == "" {
		out = append(out, "team absente (-TEAM à la fin)")
	}
	if len(out) > 0 {
		return out, ""
	}

	idx := func(kinds ...string) int {
		for i, t := range r.Tokens {
			for _, k := range kinds {
				if t.Kind == k {
					return i
				}
			}
		}
		return -1
	}
	iy, il, ir, is, ic := idx(KYear), idx(KLang), idx(KRes), idx(KProvider, KSource), idx(KCodec)
	order := true
	if il < iy {
		out = append(out, "la langue doit venir après l'année")
		order = false
	}
	if ir < il {
		out = append(out, "la langue doit venir avant la résolution")
		order = false
	}
	if is < ir {
		out = append(out, "la source doit venir après la résolution")
		order = false
	}
	if ic >= 0 && ic < is {
		out = append(out, "le codec doit venir après la source")
		order = false
	}
	if order {
		return out, ""
	}

	// Réordonné : titre, année, mentions (REPACK, EXTENDED…), langue,
	// résolution, source, REMUX, HDR, audio, codec, team.
	pick := func(kinds ...string) []string {
		var v []string
		for _, t := range r.Tokens {
			for _, k := range kinds {
				if t.Kind == k {
					v = append(v, t.Value)
				}
			}
		}
		return v
	}
	var parts []string
	parts = append(parts, pick(KTitle)...)
	parts = append(parts, pick(KYear)...)
	parts = append(parts, pick(K3D, KTag, KOther)...)
	parts = append(parts, pick(KLang, KLangVar)...)
	parts = append(parts, pick(KRes)...)
	parts = append(parts, pick(KProvider, KSource)...)
	parts = append(parts, pick(KRemux, KHDR, KAudio, KCodec)...)
	s := strings.Join(parts, ".") + "-" + r.Group
	if strings.EqualFold(s, r.Name) {
		return out, ""
	}
	return out, s
}
