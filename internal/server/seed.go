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
// reprendre) croisé avec l'état réel de tes clients (tous à la fois).

// Client : où en est la release dans ton client.
const (
	SeedOK          = "seeding"     // en seed
	SeedStopped     = "stopped"     // complète mais arrêtée
	SeedIncomplete  = "incomplete"  // arrêtée et incomplète
	SeedChecking    = "checking"    // vérification
	SeedDownloading = "downloading" // téléchargement en cours
	SeedMissing     = "missing"     // absente de tous les clients
	SeedUnknown     = "unknown"     // absente des clients joints, un autre est injoignable
)

type seedItem struct {
	v3x.HnRItem
	Client   string  `json:"client"`
	Where    string  `json:"where,omitempty"` // le client qui l'a (plusieurs clients)
	Progress float64 `json:"progress"`
	Left     int64   `json:"leftSeconds"` // seed encore requis
	Risk     string  `json:"risk"`        // ok, warn, bad
}

type relayItem struct {
	v3x.Relay
	Client string `json:"client"` // dans ton client, ou SeedMissing
	Where  string `json:"where,omitempty"`
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
	// Warn : les clients injoignables (leur état n'est pas connu).
	Warn string    `json:"warn,omitempty"`
	At   time.Time `json:"at"`
}

type stoppedItem struct {
	Hash  string `json:"hash"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Where string `json:"where,omitempty"`
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

// stateRank : du plus utile au moins utile, quand un torrent est dans
// plusieurs clients (en seed dans l'un suffit).
var stateRank = map[string]int{SeedOK: 0, SeedChecking: 1, SeedDownloading: 2, SeedStopped: 3, SeedIncomplete: 4}

// inClient : un torrent et le client où il est.
type inClient struct {
	t     client.Torrent
	where string
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
	env, err := s.env()
	if err != nil {
		return nil, err
	}
	st, err := s.seedStatus(ctx, env.V3X, fresh)
	if err != nil {
		return nil, err
	}
	lists, down := crossseed.ListAll(ctx, env)
	if len(lists) == 0 {
		return nil, errors.New(crossseed.DownText(down))
	}
	// Le nom du client n'est utile que s'il y en a plusieurs.
	multi := len(env.Cfg.Clients) > 1
	byHash := map[string]inClient{}
	for _, l := range lists {
		where := ""
		if multi {
			where = l.Target.Name
		}
		for _, t := range l.List {
			x := inClient{t, where}
			for _, h := range append([]string{t.Hash}, t.Hashes...) {
				h = strings.ToLower(h)
				if old, ok := byHash[h]; !ok || stateRank[clientState(t)] < stateRank[clientState(old.t)] {
					byHash[h] = x
				}
			}
		}
	}
	out := &seedView{At: time.Now(), RelayPrime: st.Relays.Prime, Warn: crossseed.DownText(down)}
	out.HnR.Enabled, out.HnR.Active, out.HnR.MaxActive, out.HnR.Exempt = st.HnR.Enabled, st.HnR.Active, st.HnR.MaxActive, st.HnR.Exempt
	out.HnR.MinSeedHours, out.HnR.WindowDays = st.HnR.MinSeedHours, st.HnR.WindowDays
	for _, it := range st.HnR.Items {
		x := seedItem{HnRItem: it, Client: SeedMissing}
		if t, ok := byHash[strings.ToLower(it.InfoHash)]; ok {
			x.Client, x.Progress, x.Where = clientState(t.t), t.t.Progress, t.where
		} else if len(down) > 0 {
			// Peut-être dans le client injoignable : on ne crie pas au loup.
			x.Client = SeedUnknown
		}
		if x.Left = it.RequiredSeconds - it.SeedSeconds; x.Left < 0 {
			x.Left = 0
		}
		// Risque : hit and run marqué, ou plus le temps de seeder ce qu'il reste.
		hoursLeft := time.Until(it.Deadline).Hours()
		switch {
		case it.HnR:
			x.Risk = "bad"
		case x.Client == SeedOK || x.Client == SeedChecking || x.Client == SeedUnknown:
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
			x.Client, x.Where = clientState(t.t), t.where
		} else if k, ok := addable[h]; ok {
			x.AddKey = k
		} else {
			continue // ni chez toi ni ajoutable : rien à faire pour toi
		}
		out.Relays = append(out.Relays, x)
	}
	for _, l := range lists {
		where := ""
		if multi {
			where = l.Target.Name
		}
		for _, t := range l.List {
			// En seed dans un autre client : rien à démarrer.
			if isV3X(t) && t.Stopped && t.Progress >= 1 && byHash[strings.ToLower(t.Hash)].t.Stopped {
				out.Stopped = append(out.Stopped, stoppedItem{Hash: strings.ToLower(t.Hash), Name: t.Name, Size: t.TotalSize, Where: where})
			}
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
	env, err := s.env()
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	// Chaque client démarre ceux qu'il a.
	n := 0
	var firstErr error
	for _, tg := range env.Targets {
		list, err := tg.Client.List(ctx, in.Hashes)
		if err != nil {
			if firstErr == nil {
				firstErr = errors.New(tg.Name + " : " + err.Error())
			}
			continue
		}
		for _, t := range list {
			if !isV3X(t) || t.Progress < 1 || !t.Stopped {
				continue
			}
			if err := tg.Client.Start(ctx, t.Hash); err != nil {
				if firstErr == nil {
					firstErr = errors.New("démarrage refusé par " + tg.Name + " : " + err.Error())
				}
				continue
			}
			n++
		}
	}
	if n == 0 && firstErr != nil {
		fail(w, http.StatusBadGateway, firstErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"started": n})
}
