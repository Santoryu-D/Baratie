package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/v3x"
)

// Un refus de V3X (clé sans le droit « Upload ») ne doit jamais arriver à la
// page en 401 : elle le prendrait pour une session D3X expirée.
func TestAPIFailNeverSends401(t *testing.T) {
	for _, tc := range []struct {
		in       *v3x.APIError
		contains string
	}{
		{&v3x.APIError{Status: http.StatusUnauthorized, Code: "unauthorized"}, "droit « Upload »"},
		{&v3x.APIError{Status: http.StatusForbidden, Code: "forbidden"}, "droit « Upload »"},
		{&v3x.APIError{Status: http.StatusForbidden, Code: "not_uploader"}, "not_uploader"},
	} {
		w := httptest.NewRecorder()
		apiFail(w, tc.in)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%v : code %d, attendu 403", tc.in, w.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body["error"], tc.contains) {
			t.Fatalf("%v : message %q sans %q", tc.in, body["error"], tc.contains)
		}
	}
}
