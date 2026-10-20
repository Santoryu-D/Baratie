package server

import (
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
)

// Fiches de la démo, sur le modèle de celles du site (affiche, synopsis, fiche
// film, technique, téléchargement), avec une affiche et une pochette dessinées
// ici et servies par la démo elle-même (/demo/…).

const demoPoster = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 600" width="400" height="600">
<defs>
<linearGradient id="g" x1="0" y1="0" x2="0.4" y2="1"><stop offset="0" stop-color="#0b1430"/><stop offset=".55" stop-color="#1b3a8f"/><stop offset="1" stop-color="#4d8dff"/></linearGradient>
<radialGradient id="s" cx=".7" cy=".32" r=".5"><stop offset="0" stop-color="#ffd27a" stop-opacity=".95"/><stop offset=".35" stop-color="#ff8a3d" stop-opacity=".55"/><stop offset="1" stop-color="#ff8a3d" stop-opacity="0"/></radialGradient>
</defs>
<rect width="400" height="600" fill="url(#g)"/>
<circle cx="280" cy="190" r="190" fill="url(#s)"/>
<path d="M0 430 L70 360 L120 400 L190 310 L260 390 L310 350 L400 420 L400 600 L0 600Z" fill="#081026" opacity=".92"/>
<path d="M0 470 L90 420 L160 455 L240 400 L330 450 L400 430 L400 600 L0 600Z" fill="#050a18"/>
<text x="200" y="520" text-anchor="middle" font-family="Segoe UI, Arial, sans-serif" font-weight="800" font-size="40" letter-spacing="3" fill="#ffffff">DÉMONSTRATION</text>
<text x="200" y="552" text-anchor="middle" font-family="Segoe UI, Arial, sans-serif" font-size="15" letter-spacing="2" fill="#aab9df">UN FILM QUI N'EXISTE PAS · 2024</text>
</svg>`

const demoCover = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 500 500" width="500" height="500">
<defs>
<linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#2a0f3d"/><stop offset="1" stop-color="#c2410c"/></linearGradient>
</defs>
<rect width="500" height="500" fill="url(#g)"/>
<g fill="none" stroke="#ffd9b8" stroke-opacity=".35">
<circle cx="330" cy="250" r="150" stroke-width="2"/><circle cx="330" cy="250" r="120"/><circle cx="330" cy="250" r="92"/><circle cx="330" cy="250" r="64"/>
</g>
<circle cx="330" cy="250" r="34" fill="#ffd9b8" fill-opacity=".85"/>
<text x="40" y="380" font-family="Segoe UI, Arial, sans-serif" font-weight="800" font-size="44" fill="#ffffff">Suite pour D3X</text>
<text x="40" y="420" font-family="Segoe UI, Arial, sans-serif" font-size="22" letter-spacing="4" fill="#ffd9b8">ORCHESTRE DÉMO</text>
</svg>`

func demoAssets(mux *http.ServeMux) {
	serve := func(svg string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte(svg))
		}
	}
	mux.HandleFunc("GET /demo/affiche.svg", serve(demoPoster))
	mux.HandleFunc("GET /demo/pochette.svg", serve(demoCover))
}

func bbSection(label string) string {
	return "[center][size=18][b][uicolor]✦ [/uicolor]" + strings.ToUpper(label) + "[uicolor] ✦[/uicolor][/b][/size][/center]\n"
}

const bbDivider = "\n[center][uicolor]───────────  ✦  ───────────[/uicolor][/center]\n\n"

func bbTable(rows [][2]string) string {
	var b strings.Builder
	b.WriteString("[center][table]\n")
	for _, r := range rows {
		b.WriteString("[tr][td][b]" + r[0] + "[/b][/td][td]" + r[1] + "[/td][/tr]\n")
	}
	b.WriteString("[/table][/center]\n\n")
	return b.String()
}

func demoSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f Gio", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f Mio", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d Kio", n>>10)
}

// demoFilmFiche : la fiche d'un film choisi dans TMDB.
func demoFilmFiche(releaseName string, size int64, files int, team bool) string {
	var b strings.Builder
	if team {
		b.WriteString("[center][b][size=18]Équipe Démo[/size][/b][/center]\n\n")
	}
	b.WriteString("[center][img]/demo/affiche.svg[/img]\n\n[size=32][b]Démonstration (2024)[/b][/size]\n[i]« Tout ce qui brille n'est pas encore seedé »[/i]\n\n★ 7,8/10 · Drame · Aventure · 1 h 52[/center]")
	b.WriteString(bbDivider)
	b.WriteString(bbSection("Synopsis"))
	b.WriteString("[justify]Une archiviste découvre qu'une bibliothèque entière risque de disparaître du réseau. Avec une poignée de bénévoles, elle se lance dans une course contre la montre pour que chaque fichier retrouve au moins un seeder avant l'aube.[/justify]\n\n")
	b.WriteString(bbSection("Fiche film"))
	b.WriteString(bbTable([][2]string{{"Titre original", "Demonstration"}, {"Sortie", "12 mars 2024"}, {"Réalisation", "Camille Exemple"}, {"Pays", "France"}, {"Durée", "1 h 52"}}))
	b.WriteString(bbSection("Technique"))
	b.WriteString(bbTable([][2]string{{"Vidéo", "H.264 · 1920×1080 · 23,976 i/s"}, {"Audio", "Français, Anglais · E-AC-3 5.1"}, {"Sous-titres", "Français (forcés, complets), Anglais"}, {"Source", "WEB"}}))
	b.WriteString(bbSection("Téléchargement"))
	b.WriteString(bbTable([][2]string{{"Release", releaseName}, {"Taille", demoSize(size)}, {"Fichiers", fmt.Sprint(files)}}))
	return strings.TrimSpace(b.String())
}

// demoAlbumFiche : la fiche album d'une édition MusicBrainz.
func demoAlbumFiche() string {
	var b strings.Builder
	b.WriteString("[center][img]/demo/pochette.svg[/img]\n\n[size=32][b]" + demoAlbum + "[/b][/size]\n[i]" + demoArtist + "[/i]\n\n2024 · Digital Media · FR[/center]")
	b.WriteString(bbDivider)
	b.WriteString(bbSection("Fiche album"))
	b.WriteString(bbTable([][2]string{{"Artiste", demoArtist}, {"Album", demoAlbum}, {"Année", "2024"}, {"Label", "Disques Démo"}, {"Format", "Digital Media"}, {"Pistes", fmt.Sprint(len(demoTracks))}}))
	b.WriteString(bbSection("Tracklist"))
	for i, t := range demoTracks {
		fmt.Fprintf(&b, "[b]%d.[/b] %s  [i](%d:%02d)[/i]\n", i+1, t.title, t.secs/60, t.secs%60)
	}
	return strings.TrimSpace(b.String())
}

var (
	bbSize  = regexp.MustCompile(`\[size=(\d+)\]`)
	bbImg   = regexp.MustCompile(`\[img\]([^\[]*)\[/img\]`)
	bbSpoil = regexp.MustCompile(`\[spoiler=([^\]]*)\]`)
)

// demoBBCodeHTML : un rendu BBCode simplifié, pour l'aperçu de la démo.
func demoBBCodeHTML(bb string) string {
	h := html.EscapeString(bb)
	h = bbImg.ReplaceAllStringFunc(h, func(m string) string {
		src := bbImg.FindStringSubmatch(m)[1]
		if !strings.HasPrefix(src, "/demo/") {
			return ""
		}
		return `<img src="` + src + `" alt="" class="bb-img">`
	})
	h = bbSize.ReplaceAllStringFunc(h, func(m string) string {
		n := bbSize.FindStringSubmatch(m)[1]
		px := map[string]string{"5": "26", "18": "15", "32": "30"}[n]
		if px == "" {
			px = "16"
		}
		return `<span style="font-size:` + px + `px;line-height:1.25">`
	})
	h = bbSpoil.ReplaceAllString(h, `<details class="bb-spoiler"><summary>$1</summary>`)
	for _, p := range [][2]string{
		{"[b]", "<strong>"}, {"[/b]", "</strong>"}, {"[i]", "<em>"}, {"[/i]", "</em>"},
		{"[center]", `<div class="bb-center">`}, {"[/center]", "</div>"},
		{"[justify]", `<div class="bb-justify">`}, {"[/justify]", "</div>"},
		{"[quote]", `<blockquote class="bb-quote">`}, {"[/quote]", "</blockquote>"},
		{"[/size]", "</span>"}, {"[uicolor]", `<span class="bb-ui">`}, {"[/uicolor]", "</span>"},
		{"[table]\n", `<table class="bb-table">`}, {"[table]", `<table class="bb-table">`}, {"[/table]", "</table>"},
		{"[tr]", "<tr>"}, {"[/tr]\n", "</tr>"}, {"[/tr]", "</tr>"}, {"[td]", "<td>"}, {"[/td]", "</td>"},
		{"[/spoiler]", "</details>"}, {"[code]", "<pre>"}, {"[/code]", "</pre>"},
	} {
		h = strings.ReplaceAll(h, p[0], p[1])
	}
	h = strings.ReplaceAll(h, "</div>\n", "</div>")
	return strings.ReplaceAll(h, "\n", "<br>")
}

const demoBBCodeCSS = `.prose-v3x{line-height:1.55}
.bb-center{text-align:center}
.bb-justify{text-align:justify;max-width:62ch;margin:0 auto}
.bb-ui{color:var(--accent)}
.preview .bb-img{max-width:230px;border-radius:10px;box-shadow:0 10px 30px rgb(0 0 0 / 35%)}
.bb-table{margin:4px auto 6px;border-collapse:collapse;font-size:13.5px;text-align:left}
.bb-table td{padding:5px 14px;border-bottom:1px solid var(--line)}
.bb-table td:first-child{color:var(--muted)}
.bb-quote{border-left:3px solid var(--accent);margin:8px 0;padding:6px 12px;opacity:.9}
.bb-spoiler summary{cursor:pointer;color:var(--accent)}`
