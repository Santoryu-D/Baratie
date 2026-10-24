package server

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func TestPermHint(t *testing.T) {
	denied := &fs.PathError{Op: "open", Path: "/config/D3X/config.json.tmp", Err: fs.ErrPermission}

	t.Setenv("D3X_MANAGED", "docker")
	got := withPermHint(denied)
	if !errors.Is(got, fs.ErrPermission) {
		t.Fatal("l'erreur d'origine doit rester reconnaissable")
	}
	for _, want := range []string{"config.json.tmp", "permission denied", "user: \"UID:GID\"", "docker compose up -d"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("message sans « %s » : %s", want, got)
		}
	}

	t.Setenv("D3X_MANAGED", "")
	if got := withPermHint(denied).Error(); strings.Contains(got, "docker") || !strings.Contains(got, "vérifie ses droits") {
		t.Errorf("hors Docker : %s", got)
	}

	other := errors.New("disque plein")
	if withPermHint(other) != other || withPermHint(nil) != nil {
		t.Error("une autre erreur doit passer telle quelle")
	}
}
