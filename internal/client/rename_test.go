package client

import (
	"reflect"
	"testing"
)

func TestComponentRenames(t *testing.T) {
	ops, err := componentRenames([]Rename{{0, "A/x.mkv", "B/y.mkv"}, {1, "A/n.nfo", "B/n.nfo"}, {2, "A/Sub/s.srt", "B/Subs/s.srt"}})
	want := [][2]string{{"A/Sub", "Subs"}, {"A/x.mkv", "y.mkv"}, {"A", "B"}}
	if err != nil || !reflect.DeepEqual(ops, want) {
		t.Fatalf("%v %v", err, ops)
	}
	if ops, _ := componentRenames([]Rename{{0, "film.mkv", "Film (2020).mkv"}}); !reflect.DeepEqual(ops, [][2]string{{"film.mkv", "Film (2020).mkv"}}) {
		t.Fatalf("%v", ops)
	}
	if _, err := componentRenames([]Rename{{0, "A/x.mkv", "B/C/x.mkv"}}); err == nil {
		t.Fatal("déplacement entre dossiers accepté")
	}
	if _, err := componentRenames([]Rename{{0, "A/x", "B/x"}, {1, "A/y", "C/y"}}); err == nil {
		t.Fatal("un dossier vers deux dossiers accepté")
	}
}
