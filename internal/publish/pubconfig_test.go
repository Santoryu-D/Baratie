package publish

import (
	"testing"

	"github.com/V3x0rV3x/D3X/internal/config"
)

func TestPubConfig(t *testing.T) {
	cfg := &config.Config{Category: "cross", Tag: "D3X"}
	if got := pubConfig(cfg); got != cfg || got.Category != "cross" {
		t.Fatalf("sans catégorie de publication : %+v", got)
	}
	cfg.PubCategory = " publi "
	got := pubConfig(cfg)
	if got.Category != "publi" || got.Tag != "D3X" || cfg.Category != "cross" {
		t.Fatalf("catégorie de publication : %+v (origine %+v)", got, cfg)
	}
}
