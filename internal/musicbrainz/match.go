package musicbrainz

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Local : ce que disent les pistes de la release (tags et durées).
type Local struct {
	Artist    string
	Album     string
	Year      string
	Media     string // tag MEDIA : CD, Digital Media, Vinyl…
	MBRelease string // identifiant posé par un logiciel de tag
	Durations []float64
}

// Candidate : une édition comparée aux pistes de la release.
type Candidate struct {
	Release
	// exact : même nombre de pistes et mêmes durées ; count : même nombre de
	// pistes (durées non vérifiées) ; close : durées proches ; none : autre.
	Match     string   `json:"match"`
	Reasons   []string `json:"reasons"`
	TrackDiff float64  `json:"trackDiff,omitempty"` // écart moyen des durées, en secondes
	FromTags  bool     `json:"fromTags,omitempty"`
}

// Compare confronte une édition aux pistes.
func Compare(l Local, r Release) Candidate {
	c := Candidate{Release: r, Match: "none"}
	n := len(l.Durations)
	if r.TrackCount == n {
		c.Match = "count"
		c.Reasons = append(c.Reasons, fmt.Sprintf("même nombre de pistes (%d)", n))
	} else {
		c.Reasons = append(c.Reasons, fmt.Sprintf("%d pistes ici, %d sur cette édition", n, r.TrackCount))
	}
	if c.Match == "count" && len(r.Tracks) == n && n > 0 {
		var sum, worst float64
		known := 0
		for i, t := range r.Tracks {
			d := l.Durations[i]
			if d <= 0 || t.Length <= 0 {
				continue
			}
			diff := math.Abs(d - t.Length)
			sum += diff
			worst = math.Max(worst, diff)
			known++
		}
		if known == n {
			c.TrackDiff = sum / float64(n)
			switch {
			case c.TrackDiff <= 2 && worst <= 5:
				c.Match = "exact"
				c.Reasons = append(c.Reasons, "mêmes durées, à 2 secondes près")
			case c.TrackDiff <= 6:
				c.Match = "close"
				c.Reasons = append(c.Reasons, fmt.Sprintf("durées proches (écart moyen %.0f s)", c.TrackDiff))
			default:
				c.Match = "none"
				c.Reasons = append(c.Reasons, fmt.Sprintf("durées différentes (écart moyen %.0f s)", c.TrackDiff))
			}
		}
	}
	if l.Album != "" && similarity(l.Album, r.Title) < 0.6 {
		c.Reasons = append(c.Reasons, "titre d'album différent")
	}
	if l.Artist != "" && r.Artist != "" && similarity(l.Artist, r.Artist) < 0.5 {
		c.Reasons = append(c.Reasons, "artiste différent")
	}
	return c
}

// Result : la décision pour une release (mode lot).
type Result struct {
	Best       *Candidate  `json:"best,omitempty"`
	Candidates []Candidate `json:"candidates"`
	Certain    bool        `json:"certain"`
	Reason     string      `json:"reason"`
}

// Decide cherche l'édition des pistes. Elle n'est certaine que si les tags la
// désignent et qu'elle a le même nombre de pistes, ou si la recherche trouve
// des éditions aux mêmes durées, toutes de la même œuvre.
func (c *Client) Decide(ctx context.Context, l Local) (*Result, error) {
	res := &Result{}
	if l.MBRelease != "" {
		r, err := c.Lookup(ctx, l.MBRelease)
		if err == nil {
			cand := Compare(l, *r)
			cand.FromTags = true
			cand.Reasons = append([]string{"désignée par les tags des fichiers"}, cand.Reasons...)
			res.Candidates = []Candidate{cand}
			res.Best = &res.Candidates[0]
			if cand.Match != "none" {
				res.Certain = true
				res.Reason = "édition désignée par les tags, même nombre de pistes"
				return res, nil
			}
			res.Reason = fmt.Sprintf("les tags désignent une édition de %d pistes, la release en a %d", r.TrackCount, len(l.Durations))
			return res, nil
		} else if err != ErrNotFound {
			return nil, err
		}
	}
	if l.Album == "" {
		res.Reason = "nom de l'album inconnu (ni tags ni nom lisible)"
		return res, nil
	}
	found, err := c.Search(ctx, l.Artist, l.Album, 25)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 && l.Artist != "" {
		if found, err = c.Search(ctx, "", l.Artist+" "+l.Album, 25); err != nil {
			return nil, err
		}
	}
	res.Candidates, err = c.Rank(ctx, l, found, 3)
	if err != nil {
		return nil, err
	}
	var exact []Candidate
	for _, cand := range res.Candidates {
		if cand.Match == "exact" && similarity(l.Album, cand.Title) >= 0.6 && (l.Artist == "" || similarity(l.Artist, cand.Artist) >= 0.5) {
			exact = append(exact, cand)
		}
	}
	if len(res.Candidates) > 0 {
		res.Best = &res.Candidates[0]
	}
	switch {
	case len(res.Candidates) == 0:
		res.Reason = "aucune édition trouvée sur MusicBrainz"
	case len(exact) == 0:
		res.Reason = "aucune édition aux mêmes pistes et mêmes durées"
		known := true
		for _, d := range l.Durations {
			if d <= 0 {
				known = false
			}
		}
		if !known {
			res.Reason = "durées des pistes illisibles : édition impossible à vérifier"
		}
	default:
		group := exact[0].Group
		for _, e := range exact[1:] {
			if e.Group != group {
				res.Reason = "plusieurs albums différents ont ces pistes"
				return res, nil
			}
		}
		best := exact[0]
		for _, e := range exact[1:] {
			if prefer(l, e, best) {
				best = e
			}
		}
		res.Best, res.Certain = &best, true
		res.Reason = "même tracklist et mêmes durées"
	}
	return res, nil
}

// Rank compare les éditions trouvées : celles qui ont le même nombre de
// pistes d'abord ; les premières (au plus detail) sont lues pour comparer
// les durées.
func (c *Client) Rank(ctx context.Context, l Local, found []Release, detail int) ([]Candidate, error) {
	out := make([]Candidate, 0, len(found))
	for _, r := range found {
		out = append(out, Compare(l, r))
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(l, out[i]) > rank(l, out[j]) })
	for i := range out {
		if detail <= 0 || out[i].Match != "count" {
			continue
		}
		detail--
		r, err := c.Lookup(ctx, out[i].ID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		full := *r
		full.Score = out[i].Score
		out[i] = Compare(l, full)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(l, out[i]) > rank(l, out[j]) })
	return out, nil
}

func rank(l Local, c Candidate) float64 {
	s := float64(c.Score) / 100
	switch c.Match {
	case "exact":
		s += 3
	case "close":
		s += 2
	case "count":
		s += 1
	}
	s += similarity(l.Album, c.Title)
	if l.Artist != "" {
		s += similarity(l.Artist, c.Artist) / 2
	}
	if l.Year != "" && c.Year() == l.Year {
		s += 0.2
	}
	return s
}

// prefer : a vaut mieux que b (même support que le tag MEDIA, même année).
func prefer(l Local, a, b Candidate) bool {
	media := func(c Candidate) bool {
		m, f := strings.ToLower(l.Media), strings.ToLower(c.Format)
		switch {
		case m == "":
			return false
		case strings.Contains(m, "digital") || strings.Contains(m, "web"):
			return strings.Contains(f, "digital")
		default:
			return strings.Contains(f, m)
		}
	}
	if media(a) != media(b) {
		return media(a)
	}
	if (a.Year() == l.Year) != (b.Year() == l.Year) {
		return a.Year() == l.Year
	}
	if a.Status != b.Status {
		return a.Status == "Official"
	}
	return a.Score > b.Score
}

// ---------- ressemblance des titres ----------

var fold = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a', 'æ': 'a', 'ç': 'c',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ñ': 'n', 'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o', 'œ': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ý': 'y', 'ÿ': 'y', 'ß': 's',
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	var cur []rune
	flush := func() {
		if len(cur) > 1 || (len(cur) == 1 && unicode.IsDigit(cur[0])) {
			out[string(cur)] = true
		}
		cur = cur[:0]
	}
	for _, r := range strings.ToLower(s) {
		if f, ok := fold[r]; ok {
			r = f
		}
		switch {
		case r == '\'' || r == '’':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// similarity : part des mots en commun (indice de Jaccard).
func similarity(a, b string) float64 {
	A, B := words(a), words(b)
	if len(A) == 0 || len(B) == 0 {
		return 0
	}
	n := 0
	for w := range A {
		if B[w] {
			n++
		}
	}
	return float64(n) / float64(len(A)+len(B)-n)
}
