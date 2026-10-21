package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/V3x0rV3x/D3X/internal/config"
	"github.com/V3x0rV3x/D3X/internal/journal"
)

// Fiche gardée pour le lot : gardée par release, relue telle quelle (seuls les
// champs de publication), marquée « Prête » dans le journal, oubliée quand la
// release est remise en attente.
func TestPreparedForLot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	s := NewWith("dev", config.Defaults(), "jeton-de-test-0123456789abcdef", 8733, Options{})
	path := tmp + "/Album - Titre [FLAC]"

	code, _, body := call(t, s, http.MethodPost, "/ui/batch/prepare", map[string]interface{}{
		"path": path, "source": "local", "name": "Album - Titre [FLAC]", "summary": "Audio ▸ Musique · Titre (2020)",
		"fields": map[string]string{"releaseName": "Album - Titre [FLAC 16bit-44.1kHz]", "categoryId": "42", "description": "[b]Fiche[/b]", "pirate": "x"},
	})
	if code != http.StatusOK {
		t.Fatalf("prepare : %d %s", code, body)
	}
	q := "/ui/batch/prepared?source=local&path=" + url.QueryEscape(path)
	code, out, body := call(t, s, http.MethodGet, q, nil)
	if code != http.StatusOK {
		t.Fatalf("prepared : %d %s", code, body)
	}
	f, _ := out["fields"].(map[string]interface{})
	if f["releaseName"] != "Album - Titre [FLAC 16bit-44.1kHz]" || f["categoryId"] != "42" || f["description"] != "[b]Fiche[/b]" {
		t.Fatalf("champs relus : %v", f)
	}
	if _, bad := f["pirate"]; bad {
		t.Fatal("un champ inconnu a été gardé")
	}
	if ev, ok := journal.LastByPath()[path]; !ok || ev.Kind != "batch_ready" {
		t.Fatalf("journal : %+v", ev)
	}

	if code, _, body := call(t, s, http.MethodPost, "/ui/batch/decision", map[string]string{"path": path, "source": "local", "name": "x", "kind": "batch_reset"}); code != http.StatusOK {
		t.Fatalf("reset : %d %s", code, body)
	}
	if code, _, _ := call(t, s, http.MethodGet, q, nil); code != http.StatusNotFound {
		t.Fatalf("fiche encore là après la remise en attente : %d", code)
	}
}
