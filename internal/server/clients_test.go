package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"baratie/internal/config"
	"baratie/internal/pathmap"
	"baratie/internal/qbit"
)

// fakeQbitServer : un faux qBittorrent qui accepte le mot de passe pass et
// note les mots de passe reçus.
func fakeQbitServer(t *testing.T, pass string, list []qbit.Torrent) (*httptest.Server, func() []string) {
	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			mu.Lock()
			seen = append(seen, r.Form.Get("password"))
			mu.Unlock()
			if r.Form.Get("password") != pass {
				io.WriteString(w, "Fails.")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "x", Path: "/"})
			io.WriteString(w, "Ok.")
			return
		}
		if c, err := r.Cookie("SID"); err != nil || c.Value != "x" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/app/version":
			io.WriteString(w, "v5.1.0")
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.4")
		case "/api/v2/torrents/info":
			json.NewEncoder(w).Encode(list)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func call(t *testing.T, s *Server, method, path string, body interface{}) (int, map[string]interface{}, string) {
	t.Helper()
	var rd io.Reader = strings.NewReader("{}")
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	r := httptest.NewRequest(method, path, rd)
	r.Host = "127.0.0.1:8733"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cookie", cookieName+"="+s.Token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w.Body.String()
}

// Deux clients dans les réglages : chacun sa pastille et son erreur, jamais
// un mot de passe vers la page, mot de passe gardé seulement pour la même
// adresse, et « Mon seed » prudent quand un client ne répond pas.
func TestClientsSettings(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BARATIE_CONFIG", tmp)

	pc, _ := fakeQbitServer(t, "motdepasse-pc", nil)
	box, boxSeen := fakeQbitServer(t, "un-autre", nil) // refuse le mot de passe enregistré

	cfg := config.Defaults()
	cfg.Clients = []config.ClientConfig{
		{ID: "pc", Name: "PC", URL: pc.URL, User: "admin", Pass: "motdepasse-pc", PathMap: []pathmap.Rule{{From: `/data/films`, To: "/downloads/films"}}},
		{ID: "box", Name: "Seedbox", Kind: "qbittorrent", URL: box.URL, User: "moi", Pass: "motdepasse-box"},
	}
	s := New("test", cfg, "jeton-de-test-0123456789abcdef", 8733, Options{})
	s.connect()

	code, st, raw := call(t, s, "GET", "/ui/state", nil)
	if code != http.StatusOK {
		t.Fatalf("état : %d", code)
	}
	if strings.Contains(raw, "motdepasse") {
		t.Fatalf("un mot de passe part vers la page : %s", raw)
	}
	cl := st["clients"].([]interface{})
	if len(cl) != 2 {
		t.Fatalf("clients : %v", cl)
	}
	a, b := cl[0].(map[string]interface{}), cl[1].(map[string]interface{})
	if a["ok"] != true || a["label"] != "PC" || a["hasPass"] != true || !strings.Contains(a["version"].(string), "qBittorrent") {
		t.Fatalf("PC : %v", a)
	}
	if b["ok"] != false || !strings.Contains(b["error"].(string), "refusé") || b["hasPass"] != true {
		t.Fatalf("Seedbox : %v", b)
	}

	// Essai d'un client sans l'enregistrer : le mot de passe gardé ne part
	// qu'à la même adresse.
	if code, out, _ := call(t, s, "POST", "/ui/client/test", map[string]interface{}{"id": "pc", "url": pc.URL, "user": "admin"}); code != 200 || out["ok"] != true {
		t.Fatalf("essai du PC : %d %v", code, out)
	}
	before := len(boxSeen())
	if code, out, _ := call(t, s, "POST", "/ui/client/test", map[string]interface{}{"id": "pc", "url": box.URL + "/", "user": "admin"}); code != 200 || out["ok"] != false {
		t.Fatalf("essai ailleurs : %d %v", code, out)
	}
	for _, p := range boxSeen()[before:] {
		if p != "" {
			t.Fatalf("mot de passe du PC envoyé à une autre adresse : %q", p)
		}
	}

	// Enregistrement : mot de passe vide = gardé (même adresse), perdu pour
	// une autre adresse ; un client neuf reçoit son identifiant.
	nas, _ := fakeQbitServer(t, "nas", nil)
	in := map[string]interface{}{"clients": []map[string]interface{}{
		{"id": "pc", "name": "PC", "url": pc.URL, "user": "admin", "pathMap": []pathmap.Rule{{From: `/data/films`, To: "/downloads/films"}}},
		{"id": "box", "name": "Seedbox", "url": box.URL + "/autre", "user": "moi"},
		{"id": "nouveau", "name": "NAS", "kind": "transmission", "url": nas.URL, "pass": "nas"},
	}}
	if code, _, raw := call(t, s, "POST", "/ui/settings", in); code != 200 || strings.Contains(raw, "motdepasse") {
		t.Fatalf("réglages : %d %s", code, raw)
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Clients) != 3 || saved.Clients[0].Pass != "motdepasse-pc" || saved.Clients[1].Pass != "" || saved.Clients[2].Pass != "nas" || saved.Clients[2].Kind != "transmission" {
		t.Fatalf("clients enregistrés : %+v", saved.Clients)
	}
	if id := saved.Clients[2].ID; id == "" || id == "nouveau" || id == "pc" || id == "box" {
		t.Fatalf("identifiant du client neuf : %q", id)
	}
	if len(saved.Clients[0].PathMap) != 1 {
		t.Fatalf("dossiers : %v", saved.Clients[0].PathMap)
	}

	// Refus : deux fois le même nom, plus de huit clients.
	twice := map[string]interface{}{"clients": []map[string]interface{}{{"name": "PC", "url": pc.URL}, {"name": "pc", "url": nas.URL}}}
	if code, out, _ := call(t, s, "POST", "/ui/settings", twice); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "noms différents") {
		t.Fatalf("même nom : %d %v", code, out)
	}
	var many []map[string]interface{}
	for i := 0; i < 9; i++ {
		many = append(many, map[string]interface{}{"url": pc.URL})
	}
	if code, out, _ := call(t, s, "POST", "/ui/settings", map[string]interface{}{"clients": many}); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "8 clients") {
		t.Fatalf("neuf clients : %d %v", code, out)
	}
}

func TestClientLabels(t *testing.T) {
	got := clientLabels([]config.ClientConfig{{ID: "a"}, {ID: "b", Kind: "deluge"}, {ID: "c"}, {ID: "d", Name: "Seedbox"}})
	want := map[string]string{"a": "qBittorrent", "b": "Deluge", "c": "qBittorrent 2", "d": "Seedbox"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s : %q, attendu %q", k, got[k], v)
		}
	}
}
