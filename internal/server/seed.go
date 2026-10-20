package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/crossseed"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// « Mon seed » : ce que V3X attend de ton seed (hit and run à régler, relais à
// reprendre) croisé avec l'état réel de ton client.

// Client : où en est la release dans ton client.
const (
	SeedOK          = "seeding"     // en seed
	SeedStopped     = "stopped"     // complète mais arrêtée
	SeedIncomplete  = "incomplete"  // arrêtée et incomplète
	SeedChecking    = "checking"    // vérification
	SeedDownloading = "downloading" // téléchargement en cours
	SeedMissing     = "missing"     // absente du client
)

type seedItem struct {
	v3x.HnRItem
	Client   string  `json:"client"`
	Progress float64 `json:"progress"`
	Left     int64   `json:"leftSeconds"` // seed encore requis
	Risk     string  `json:"risk"`        // ok, warn, bad
}

type relayItem struct {
	v3x.Relay
	Client string `json:"client"` // dans ton client, ou SeedMissing
	AddKey string `json:"addKey,omitempty"`
}

type seedView struct {
	HnR struct {
		Enabled      bool `json:"enabled"`
		Active       int  `json:"active"`
		MaxActive    int  `json:"maxActive"`
		Exempt       bool `json:"exempt"`
		MinSeedHours int  `json:"minSeedHours"`
		WindowDays   int  `json:"windowDays"`
	} `json:"hnr"`
	Items      []seedItem  `json:"items"`
	Relays     []relayItem `json:"relays"`
	RelayPrime int         `json:"relayPrime"`
	// Torrents V3X complets mais arrêtés dans ton client (ils ne seedent pas).
	Stopped []stoppedItem `json:"stopped"`
	At      time.Time     `json:"at"`
}

type stoppedItem struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Cache de l'état du site (2 minutes) : la vue se rafraîchit sans le charger.
type seedCache struct {
	mu   sync.Mutex
	at   time.Time
	data *v3x.SeedStatus
}

func (s *Server) routesSeed() {
	s.mux.HandleFunc("GET /ui/seed", s.seedGet)
	s.mux.HandleFunc("POST /ui/seed/start", s.seedStart)
	s.mux.HandleFunc("GET /ui/relays", s.relaysGet)
}

// relaysGet : les relais ouverts, par info hash, pour les signaler dans
// « À ajouter » (la prime va aux premiers repreneurs).
func (s *Server) relaysGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.v3xClient()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	st, err := s.seedStatus(ctx, v, false)
	if err != nil {
		apiFail(w, err)
		return
	}
	by := map[string]v3x.Relay{}
	for _, rl := range st.Relays.Items {
		by[strings.ToLower(rl.InfoHash)] = rl
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"prime": st.Relays.Prime, "maxPaid": st.Relays.MaxPaid, "sustainHours": st.Relays.SustainHours, "byHash": by})
}

func (s *Server) seedStatus(ctx context.Context, v *v3x.Client, fresh bool) (*v3x.SeedStatus, error) {
	s.seedC.mu.Lock()
	defer s.seedC.mu.Unlock()
	if !fresh && s.seedC.data != nil && time.Since(s.seedC.at) < 2*time.Minute {
		return s.seedC.data, nil
	}
	st, err := v.SeedStatus(ctx)
	if err != nil {
		return nil, err
	}
	s.seedC.data, s.seedC.at = st, time.Now()
	return st, nil
}

// clientState : l'état d'un torrent du client, sous la forme de la vue.
func clientState(t client.Torrent) string {
	switch {
	case t.Checking:
		return SeedChecking
	case t.Downloading:
		return SeedDownloading
	case t.Stopped && t.Progress >= 1:
		return SeedStopped
	case t.Stopped || t.Progress < 1:
		return SeedIncomplete
	}
	return SeedOK
}

func isV3X(t client.Torrent) bool {
	for _, h := range t.Trackers {
		if crossseed.IsV3XHost(h) {
			return true
		}
	}
	return false
}

func (s *Server) buildSeed(ctx context.Context, fresh bool) (*seedView, error) {
	q, v, _, err := s.clients()
	if err != nil {
		return nil, err
	}
	st, err := s.seedStatus(ctx, v, fresh)
	if err != nil {
		return nil, err
	}
	list, err := q.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	byHash := map[string]client.Torrent{}
	for _, t := range list {
		byHash[strings.ToLower(t.Hash)] = t
		for _, h := range t.Hashes {
			byHash[strings.ToLower(h)] = t
		}
	}
	out := &seedView{At: time.Now(), RelayPrime: st.Relays.Prime}
	out.HnR.Enabled, out.HnR.Active, out.HnR.MaxActive, out.HnR.Exempt = st.HnR.Enabled, st.HnR.Active, st.HnR.MaxActive, st.HnR.Exempt
	out.HnR.MinSeedHours, out.HnR.WindowDays = st.HnR.MinSeedHours, st.HnR.WindowDays
	for _, it := range st.HnR.Items {
		x := seedItem{HnRItem: it, Client: SeedMissing}
		if t, ok := byHash[strings.ToLower(it.InfoHash)]; ok {
			x.Client, x.Progress = clientState(t), t.Progress
		}
		if x.Left = it.RequiredSeconds - it.SeedSeconds; x.Left < 0 {
			x.Left = 0
		}
		// Risque : hit and run marqué, ou plus le temps de seeder ce qu'il reste.
		hoursLeft := time.Until(it.Deadline).Hours()
		switch {
		case it.HnR:
			x.Risk = "bad"
		case x.Client == SeedOK || x.Client == SeedChecking:
			x.Risk = "ok"
			if float64(x.Left)/3600 > hoursLeft {
				x.Risk = "bad"
			}
		default:
			x.Risk = "warn"
			if float64(x.Left)/3600 > hoursLeft-6 {
				x.Risk = "bad"
			}
		}
		out.Items = append(out.Items, x)
	}
	// Les plus urgents d'abord.
	rank := map[string]int{"bad": 0, "warn": 1, "ok": 2}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if rank[out.Items[i].Risk] != rank[out.Items[j].Risk] {
			return rank[out.Items[i].Risk] < rank[out.Items[j].Risk]
		}
		return out.Items[i].Deadline.Before(out.Items[j].Deadline)
	})
	// Relais : ceux que tu as dans ton client, ou que tu peux y ajouter en
	// cross-seed (même release chez un autre tracker).
	addable := map[string]string{}
	for _, r := range s.engine.Rows() {
		if r.Match != nil && r.Status == crossseed.StAdd {
			addable[strings.ToLower(r.Match.InfoHash)] = r.Key
		}
	}
	for _, rl := range st.Relays.Items {
		h := strings.ToLower(rl.InfoHash)
		x := relayItem{Relay: rl, Client: SeedMissing}
		if t, ok := byHash[h]; ok {
			x.Client = clientState(t)
		} else if k, ok := addable[h]; ok {
			x.AddKey = k
		} else {
			continue // ni chez toi ni ajoutable : rien à faire pour toi
		}
		out.Relays = append(out.Relays, x)
	}
	for _, t := range list {
		if isV3X(t) && t.Stopped && t.Progress >= 1 {
			out.Stopped = append(out.Stopped, stoppedItem{Hash: strings.ToLower(t.Hash), Name: t.Name, Size: t.TotalSize})
		}
	}
	sort.Slice(out.Stopped, func(i, j int) bool { return out.Stopped[i].Name < out.Stopped[j].Name })
	return out, nil
}

func (s *Server) seedGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	view, err := s.buildSeed(ctx, r.URL.Query().Get("fresh") == "1")
	if err != nil {
		apiFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// seedStart démarre des torrents V3X COMPLETS arrêtés (jamais un incomplet :
// ce serait un téléchargement, à lancer soi-même).
func (s *Server) seedStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hashes []string `json:"hashes"`
	}
	if !decode(w, r, 1<<16, &in) {
		return
	}
	q, _, _, err := s.clients()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	list, err := q.List(ctx, in.Hashes)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	n := 0
	for _, t := range list {
		if !isV3X(t) || t.Progress < 1 || !t.Stopped {
			continue
		}
		if err := q.Start(ctx, t.Hash); err != nil {
			fail(w, http.StatusBadGateway, errors.New("démarrage refusé par le client : "+err.Error()))
			return
		}
		n++
	}
	writeJSON(w, http.StatusOK, map[string]int{"started": n})
}
