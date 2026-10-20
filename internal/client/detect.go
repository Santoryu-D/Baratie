package client

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Found est un client torrent trouvé sur ce poste.
type Found struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

// Detect sonde les ports habituels de ce poste et ne garde que ce qui répond
// avec la signature d'un client (une page quelconque sur le port 8080 n'est
// pas qBittorrent).
func Detect() []Found {
	hc := &http.Client{Timeout: 1500 * time.Millisecond}
	probes := []struct {
		kind, name, url string
		ok              func(*http.Client, string) bool
	}{
		{"qbittorrent", "qBittorrent", "http://127.0.0.1:8080", isQbit},
		{"qbittorrent", "qBittorrent", "http://127.0.0.1:8090", isQbit},
		{"transmission", "Transmission", "http://127.0.0.1:9091", isTransmission},
		{"deluge", "Deluge", "http://127.0.0.1:8112", isDeluge},
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []Found
	)
	for _, p := range probes {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.ok(hc, p.url) {
				mu.Lock()
				out = append(out, Found{Kind: p.kind, URL: p.url, Name: p.name})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

func readSome(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 64<<10))
	return string(b)
}

func isQbit(hc *http.Client, base string) bool {
	resp, err := hc.Get(base + "/api/v2/app/webapiVersion")
	if err != nil {
		return false
	}
	body := readSome(resp.Body)
	resp.Body.Close()
	// Connecté (ou liste blanche) : « 2.11.2 » ; sinon 403 « Forbidden »,
	// alors la page d'accueil doit être celle de qBittorrent.
	if resp.StatusCode == http.StatusOK && len(body) < 16 && strings.Count(body, ".") >= 1 {
		return true
	}
	resp, err = hc.Get(base + "/")
	if err != nil {
		return false
	}
	home := readSome(resp.Body)
	resp.Body.Close()
	return strings.Contains(home, "qBittorrent")
}

func isTransmission(hc *http.Client, base string) bool {
	resp, err := hc.Post(base+"/transmission/rpc", "application/json", strings.NewReader(`{"method":"session-get"}`))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.Header.Get("X-Transmission-Session-Id") != ""
}

func isDeluge(hc *http.Client, base string) bool {
	resp, err := hc.Post(base+"/json", "application/json", bytes.NewReader([]byte(`{"method":"auth.check_session","params":[],"id":1}`)))
	if err != nil {
		return false
	}
	body := readSome(resp.Body)
	resp.Body.Close()
	return strings.Contains(body, `"result"`) && strings.Contains(body, `"id"`)
}
