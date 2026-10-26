package pantry

// Baratie 0.6.0 : lecture des films en fond (ffprobe, en-têtes seulement,
// lecture seule) pour appliquer les règles du tracker dans le Garde-manger, et
// langue originale demandée à TMDB pour les films identifiés avant la 0.6.0.

import (
	"context"
	"os"
	"time"

	"baratie/internal/probe"
)

// pkick réveille la lecture des fichiers.
func (m *Manager) pkick() {
	select {
	case m.pwake <- struct{}{}:
	default:
	}
}

// needsProbe : le fichier n'a jamais été lu, ou il a changé depuis.
func needsProbe(it *Item) bool {
	return !it.Ignored && it.ProbeKey != it.probeKey()
}

// needsLang : film identifié dont on ignore la langue originale.
func needsLang(it *Item) bool {
	return !it.Ignored && it.TMDB > 0 && !it.LangChecked
}

func (m *Manager) probeCounts() (done, todo int) {
	libs := Libs(m.deps.Cfg())
	for _, it := range m.items {
		if !under(it.Path, libs) || it.Ignored {
			continue
		}
		if needsProbe(it) {
			todo++
		} else {
			done++
		}
	}
	return
}

// nextProbe : le prochain film à lire (ordre des chemins, stable).
func (m *Manager) nextProbe(lang bool) *Item {
	libs := Libs(m.deps.Cfg())
	var best *Item
	for _, it := range m.items {
		if !under(it.Path, libs) {
			continue
		}
		if (lang && needsLang(it)) || (!lang && needsProbe(it)) {
			if best == nil || it.Path < best.Path {
				best = it
			}
		}
	}
	return best
}

// prober : un fichier à la fois ; ffprobe ne lit que les en-têtes.
func (m *Manager) prober() {
	for {
		worked := false
		// Langue originale (TMDB) des films identifiés avant la 0.6.0.
		if c := m.deps.TMDB(); c != nil {
			m.mu.Lock()
			it := m.nextProbe(true)
			id := 0
			if it != nil {
				id = it.TMDB
			}
			m.mu.Unlock()
			if it != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				mv, err := c.Details(ctx, id)
				cancel()
				rules, _ := m.rules()
				m.mu.Lock()
				if it.TMDB == id {
					if err == nil {
						it.OrigLang, it.Animation = mv.OrigLang, mv.Animation
					}
					it.LangChecked = true // une erreur ne relance pas en boucle
					m.reclass(it, rules)
					m.dirty = true
				}
				m.mu.Unlock()
				time.Sleep(m.tmdbGap)
				worked = true
			}
		}

		ff := ""
		if m.deps.FFprobe != nil {
			ff = m.deps.FFprobe()
		}
		m.mu.Lock()
		if ff == "" {
			m.st.ProbeOff = "ffprobe absent : les fichiers ne sont pas lus (image Docker 0.4 ou plus)"
		} else {
			m.st.ProbeOff = ""
		}
		var it *Item
		if ff != "" {
			it = m.nextProbe(false)
		}
		var path, key string
		if it != nil {
			path, key = it.Path, it.probeKey()
			m.st.Probing = it.Name
		}
		m.mu.Unlock()
		if it != nil {
			var errText string
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			mi, err := probe.File(ctx, ff, path)
			cancel()
			if err != nil {
				errText = err.Error()
				if _, serr := os.Stat(path); serr != nil {
					errText = "fichier introuvable"
				}
			}
			rules, _ := m.rules()
			m.mu.Lock()
			if cur := m.items[it.ID]; cur == it && it.Path == path {
				it.ProbeKey, it.ProbeErr = key, errText
				if err == nil {
					cp := mi
					it.Probe = &cp
				} else {
					it.Probe = nil
				}
				m.reclass(it, rules)
				m.dirty = true
			}
			m.st.Probing = ""
			m.mu.Unlock()
			worked = true
		}
		if !worked {
			select {
			case <-m.pwake:
			case <-time.After(time.Minute):
			}
		}
	}
}
