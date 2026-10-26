package config

import (
	"testing"
)

func TestClientIDs(t *testing.T) {
	c := Defaults()
	if err := Parse([]byte(`{"clients":[{"url":"http://a"},{"id":"x","url":"http://b"},{"id":"x","url":"http://c"}]}`), c); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, cl := range c.Clients {
		if cl.ID == "" || seen[cl.ID] {
			t.Fatalf("identifiants : %+v", c.Clients)
		}
		seen[cl.ID] = true
	}
	if c.Clients[1].ID != "x" {
		t.Fatalf("un identifiant valable ne change pas : %+v", c.Clients)
	}
	if a, b := NewClientID(), NewClientID(); a == b || len(a) != 12 {
		t.Fatalf("NewClientID : %s %s", a, b)
	}
}

func TestDir(t *testing.T) {
	d := t.TempDir()
	t.Setenv("BARATIE_CONFIG", d)
	c := Defaults()
	c.TrackerURL, c.Clients = "https://tracker.exemple", []ClientConfig{{URL: "http://qbit:8080"}}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || got.TrackerURL != c.TrackerURL || len(got.Clients) != 1 || got.Clients[0].ID == "" || got.Tag != "Baratie" {
		t.Fatalf("%+v %v", got, err)
	}
}
