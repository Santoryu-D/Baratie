package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/config"
)

func testServer(t *testing.T, opts Options) *Server {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	return NewWith("dev", config.Defaults(), "jeton-de-test-0123456789abcdef", 8733, opts)
}

func do(s *Server, method, host, path string, h map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.Host = host
	for k, v := range h {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// Sur ce PC : seuls 127.0.0.1, localhost et [::1] au bon port (rebond DNS).
func TestLocalHosts(t *testing.T) {
	s := testServer(t, Options{})
	for host, want := range map[string]int{
		"127.0.0.1:8733": http.StatusOK,
		"localhost:8733": http.StatusOK,
		"[::1]:8733":     http.StatusOK,
		"evil.test:8733": http.StatusForbidden,
		"127.0.0.1:9999": http.StatusForbidden,
		"nas.local":      http.StatusForbidden,
	} {
		if got := do(s, "GET", host, "/health", nil).Code; got != want {
			t.Errorf("%s : %d, attendu %d", host, got, want)
		}
	}
}

// Mode serveur : tout nom d'hôte, mais le jeton reste exigé.
func TestServerMode(t *testing.T) {
	s := testServer(t, Options{Remote: true, Managed: "docker"})
	if got := do(s, "GET", "nas.local:8733", "/health", nil).Code; got != http.StatusOK {
		t.Fatalf("hôte du réseau refusé : %d", got)
	}
	if got := do(s, "GET", "nas.local:8733", "/ui/state", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("page sans cookie : %d", got)
	}
	if got := do(s, "GET", "nas.local:8733", "/auth?t=faux", nil).Code; got != http.StatusForbidden {
		t.Fatalf("mauvais jeton : %d", got)
	}
	w := do(s, "GET", "d3x.exemple.net", "/auth?t=jeton-de-test-0123456789abcdef", map[string]string{"X-Forwarded-Proto": "https"})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("bon jeton : %d", w.Code)
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].MaxAge < 300*24*3600 || !c[0].Secure || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie : %+v", c)
	}
	cookie := map[string]string{"Cookie": c[0].Name + "=" + c[0].Value}
	w = do(s, "GET", "d3x.exemple.net", "/ui/state", cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mode":"docker"`) {
		t.Fatalf("état : %d %s", w.Code, w.Body.String())
	}
	// Derrière un proxy https : l'origine https du même hôte est acceptée,
	// une autre origine non.
	post := func(origin string) int {
		h := map[string]string{"Cookie": cookie["Cookie"], "Content-Type": "application/json", "Origin": origin}
		return do(s, "POST", "d3x.exemple.net", "/ui/stop", h).Code
	}
	if got := post("https://d3x.exemple.net"); got == http.StatusForbidden {
		t.Fatalf("origine https refusée")
	}
	if got := post("https://evil.test"); got != http.StatusForbidden {
		t.Fatalf("autre origine acceptée : %d", got)
	}
}

func TestMaskURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://box:secret@seedbox.test/RPC2": "https://box:***@seedbox.test/RPC2",
		"http://127.0.0.1:8080":                "http://127.0.0.1:8080",
		"https://user@seedbox.test/":           "https://user@seedbox.test/",
	} {
		if got := maskURL(in); got != want {
			t.Errorf("%s : %s, attendu %s", in, got, want)
		}
	}
}
