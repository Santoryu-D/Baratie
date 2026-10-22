package crossseed

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/V3x0rV3x/D3X/internal/client"
	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/torrent"
	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// « Démarrer le seed à la fin du lot » (karat, 22/10/26) : tant que le bail
// court, une publication vérifiée à 100 % reste à l'arrêt ; un cross-seed
// démarre comme d'habitude. Bail levé : la publication démarre seule.
func TestHoldPublished(t *testing.T) {
	isolatedConfig(t)
	pub := mkTorrent("Release.P", map[string]int64{"P.mkv": 1_000_000_000}, "https://announce.v3x.club/pk/announce", "")
	cross := mkTorrent("Release.X", map[string]int64{"X.mkv": 2_000_000_000}, "https://announce.v3x.club/pk/announce", "")
	pp, _ := torrent.Parse(pub)
	px, _ := torrent.Parse(cross)
	hp, hx := strings.ToLower(pp.InfoHash), strings.ToLower(px.InfoHash)
	fq := &fakeQbit{list: map[string]*fakeTorrent{}, complete: map[string]float64{hp: 1, hx: 1}}
	qs := httptest.NewServer(fq.handler(t))
	defer qs.Close()
	vs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			io.WriteString(w, `{"username":"test","ratio":2.5,"hitAndRun":0}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer vs.Close()
	q, _ := client.New(client.Options{URL: qs.URL, User: "admin", Pass: "secret"})
	clients := oneClient(q, v3x.New(vs.URL, "cle", "D3X/test"), config.Defaults())
	e := New(clients)
	env, err := clients()
	if err != nil {
		t.Fatal(err)
	}
	tg := env.Targets[0]
	started := func(h string) bool {
		fq.mu.Lock()
		defer fq.mu.Unlock()
		return slices.Contains(fq.started, h)
	}

	e.HoldPublished(time.Minute)
	ctx := context.Background()
	if _, err := e.AddAndVerify(ctx, tg, env.Cfg, "pub:1", "11111111-1111-1111-1111-111111111111", pub, "/data"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddAndVerify(ctx, tg, env.Cfg, "cross", "22222222-2222-2222-2222-222222222222", cross, "/data"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		st, _ := e.WatchOf(hx)
		return st.Status == StSeeding
	})
	waitFor(t, func() bool {
		st, _ := e.WatchOf(hp)
		return st.Status == StChecking && st.Detail == HeldDetail
	})
	time.Sleep(5 * watchEvery) // plusieurs passages : toujours à l'arrêt
	if started(hp) {
		t.Fatal("publication démarrée alors que le bail court")
	}
	if !started(hx) {
		t.Fatal("cross-seed retenu par le bail des publications")
	}

	e.HoldPublished(0)
	waitFor(t, func() bool {
		st, _ := e.WatchOf(hp)
		return st.Status == StSeeding && started(hp)
	})
}
