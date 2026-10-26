// Package journal tient l'historique de ce que Baratie a fait (history.jsonl,
// une ligne JSON par événement, à côté de la configuration). Aucun secret.
package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"

	"baratie/internal/config"
)

// Event est une ligne du journal.
type Event struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"` // crossseed, crossseed_failed, removed
	Name     string    `json:"name"`
	InfoHash string    `json:"infoHash,omitempty"`
	ID       string    `json:"id,omitempty"` // fiche sur le tracker
	Detail   string    `json:"detail,omitempty"`
	Path     string    `json:"path,omitempty"` // release locale (publication, lot)
}

var mu sync.Mutex

const file = "history.jsonl"

// Add ajoute un événement.
func Add(e Event) {
	mu.Lock()
	defer mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	p, err := config.Path(file)
	if err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

// Last renvoie les n derniers événements, du plus récent au plus ancien.
func Last(n int) []Event {
	mu.Lock()
	defer mu.Unlock()
	p, err := config.Path(file)
	if err != nil {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}
	out := make([]Event, 0, n)
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out
}

// LastByPath renvoie, pour chaque release locale, son dernier événement
// (publication, décision du lot) : le lot ne repropose pas ce qui est fait.
func LastByPath() map[string]Event {
	mu.Lock()
	defer mu.Unlock()
	out := map[string]Event{}
	p, err := config.Path(file)
	if err != nil {
		return out
	}
	f, err := os.Open(p)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Path != "" {
			out[e.Path] = e
		}
	}
	return out
}
