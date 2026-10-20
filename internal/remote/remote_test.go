package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/V3x0rV3x/D3X/internal/agent"
	"github.com/V3x0rV3x/D3X/internal/maketorrent"
	"github.com/V3x0rV3x/D3X/internal/releases"
	"github.com/V3x0rV3x/D3X/internal/remote/sshtest"
	"github.com/V3x0rV3x/D3X/internal/torrent"
)

const pass = "mot-de-passe"

func boxConfig(b *sshtest.Box) Config {
	return Config{Host: b.Host(), Port: b.Port(), User: sshtest.User, KeyPath: filepath.Join(os.TempDir(), "d3x-aucune-cle")}
}

func isolate(t *testing.T) string {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	agentBinary = func(arch string) ([]byte, error) {
		if strings.TrimSpace(arch) != "x86_64" {
			return nil, errors.New("arch")
		}
		return []byte("agent de test"), nil
	}
	t.Cleanup(func() { agentBinary = agentFor })
	return tmp
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

// Clé d'hôte : inconnue → empreinte à accepter ; changée → bloqué.
func TestHostKey(t *testing.T) {
	isolate(t)
	b := sshtest.New(t, pass)
	cfg := boxConfig(b)

	c := New(cfg, pass, "test")
	_, err := c.Test(ctx(t))
	var hk *HostKeyError
	if !errors.As(err, &hk) || hk.Changed {
		t.Fatalf("première connexion : %v", err)
	}
	if hk.Fingerprint != ssh.FingerprintSHA256(b.HostKey.PublicKey()) || hk.Key != b.HostLine() {
		t.Fatalf("empreinte %s, clé %s", hk.Fingerprint, hk.Key)
	}

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	os2, _ := ssh.NewSignerFromKey(other)
	cfg.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(os2.PublicKey())))
	_, err = New(cfg, pass, "test").Test(ctx(t))
	if !errors.As(err, &hk) || !hk.Changed {
		t.Fatalf("clé changée non bloquée : %v", err)
	}

	cfg.HostKey = hk.Key
	sys, err := New(cfg, pass, "test").Test(ctx(t))
	if err != nil || sys != "Linux x86_64" {
		t.Fatalf("clé acceptée : %q %v", sys, err)
	}
}

func TestAuth(t *testing.T) {
	tmp := isolate(t)
	b := sshtest.New(t, pass)
	cfg := boxConfig(b)
	cfg.HostKey = b.HostLine()

	if _, err := New(cfg, "faux", "test").Test(ctx(t)); err == nil || !strings.Contains(err.Error(), "refuse") {
		t.Fatalf("mauvais mot de passe : %v", err)
	}
	if _, err := New(cfg, "", "test").Test(ctx(t)); err == nil || !strings.Contains(err.Error(), "aucune clé") {
		t.Fatalf("sans clé ni mot de passe : %v", err)
	}

	// Clé protégée par une phrase de passe (le secret sert à l'ouvrir).
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	blk, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("phrase"))
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(tmp, "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	uk, _ := ssh.NewPublicKey(pub)
	b.SetUserKey(uk)
	b.SetPass("autre")
	cfg.KeyPath = keyPath
	if _, err := New(cfg, "phrase", "test").Test(ctx(t)); err != nil {
		t.Fatalf("clé protégée : %v", err)
	}
	if _, err := New(cfg, "", "test").Test(ctx(t)); err == nil {
		t.Fatal("clé protégée ouverte sans sa phrase de passe")
	}
}

// L'agent est déposé une fois, vérifié, et fait le même travail qu'en local :
// même liste, même plan, même .torrent (même info-hash), mêmes octets lus.
func TestAgentMatchesLocal(t *testing.T) {
	tmp := isolate(t)
	b := sshtest.New(t, pass)
	cfg := boxConfig(b)
	cfg.HostKey = b.HostLine()

	lib := filepath.Join(tmp, "films")
	rel := filepath.Join(lib, "Film.2020.1080p.WEB.x264")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	video := make([]byte, 3<<20+12345)
	rand.Read(video)
	os.WriteFile(filepath.Join(rel, "Film.2020.1080p.WEB.x264.mkv"), video, 0o644)
	os.WriteFile(filepath.Join(rel, "Film.2020.1080p.WEB.x264.nfo"), []byte("nfo"), 0o644)
	os.WriteFile(filepath.Join(rel, "Thumbs.db"), []byte("x"), 0o644)
	exclude := maketorrent.DefaultExclude

	c := New(cfg, pass, "test")
	defer c.Close()

	var l releases.Listing
	if err := c.AgentJSON(ctx(t), "ls", agent.Request{Path: lib, Exclude: exclude}, &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 1 || l.Entries[0].Name != "Film.2020.1080p.WEB.x264" {
		t.Fatalf("ls : %+v", l.Entries)
	}

	var list []releases.Entry
	if err := c.AgentJSON(ctx(t), "list", agent.Request{Path: lib, Exclude: exclude}, &list); err != nil {
		t.Fatal(err)
	}
	want, _ := releases.List(lib, exclude)
	if len(list) != 1 || list[0].Size != want[0].Size || list[0].Files != want[0].Files || !list[0].Video {
		t.Fatalf("list : %+v, attendu %+v", list, want)
	}

	var plan maketorrent.Plan
	if err := c.AgentJSON(ctx(t), "scan", agent.Request{Path: rel, Exclude: exclude}, &plan); err != nil {
		t.Fatal(err)
	}
	local, err := maketorrent.Scan(rel, exclude)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Total != local.Total || len(plan.Files) != len(local.Files) || plan.PieceLength != local.PieceLength {
		t.Fatalf("scan : %d fichiers %d octets, attendu %d / %d", len(plan.Files), plan.Total, len(local.Files), local.Total)
	}

	var last [2]int64
	cached := false
	raw, err := c.Agent(ctx(t), "hash", agent.Request{Path: rel, Exclude: exclude, PieceLength: plan.PieceLength, CreatedBy: "D3X test"}, func(d, n int64) { last = [2]int64{d, n} }, &cached)
	if err != nil {
		t.Fatal(err)
	}
	if cached || last[0] != local.Total || last[1] != local.Total {
		t.Fatalf("avancement %v (total %d), cache %v", last, local.Total, cached)
	}
	// Deuxième fois : repris du cache de la seedbox.
	if _, err := c.Agent(ctx(t), "hash", agent.Request{Path: rel, Exclude: exclude, PieceLength: plan.PieceLength, CreatedBy: "D3X test"}, nil, &cached); err != nil || !cached {
		t.Fatalf("cache : %v %v", cached, err)
	}
	os.RemoveAll(filepath.Join(tmp, "D3X"))
	localRaw, _, err := local.Hash(ctx(t), "D3X test", nil)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := torrent.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := torrent.Parse(localRaw)
	if tr.InfoHash != tl.InfoHash || tr.ContentHash != tl.ContentHash {
		t.Fatalf("info-hash %s, attendu %s", tr.InfoHash, tl.InfoHash)
	}

	mkv := ""
	for _, f := range plan.Files {
		if strings.HasSuffix(f.Rel, ".mkv") {
			mkv = f.Abs
		}
	}
	part, err := c.Agent(ctx(t), "read", agent.Request{Path: mkv, Offset: 1 << 20, Size: 4096}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(part, video[1<<20:1<<20+4096]) {
		t.Fatal("morceau lu différent")
	}

	// Un seul dépôt, même avec une nouvelle connexion.
	c2 := New(cfg, pass, "test")
	defer c2.Close()
	if _, err := c2.Agent(ctx(t), "version", agent.Request{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := b.Uploads(); n != 1 {
		t.Fatalf("%d dépôts de l'agent", n)
	}
	// Aucun chemin de fichier dans les commandes : tout passe en base64.
	for _, cmd := range b.Commands() {
		if strings.Contains(cmd, "Film.2020") {
			t.Fatalf("chemin en clair dans la commande : %s", cmd)
		}
	}
}

// Album sur la seedbox : l'agent lit les tags et la qualité sur place.
func TestAgentAudio(t *testing.T) {
	tmp := isolate(t)
	b := sshtest.New(t, pass)
	cfg := boxConfig(b)
	cfg.HostKey = b.HostLine()
	dir := filepath.Join(tmp, "Artiste - Album (2020)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// WAV 24 bits, 48 kHz, stéréo, 1 seconde.
	wav := func() []byte {
		le := func(v uint32, n int) []byte {
			out := make([]byte, n)
			for i := range out {
				out[i] = byte(v >> (8 * i))
			}
			return out
		}
		data := make([]byte, 48000*6)
		fmtc := bytes.Join([][]byte{le(1, 2), le(2, 2), le(48000, 4), le(48000*6, 4), le(6, 2), le(24, 2)}, nil)
		return bytes.Join([][]byte{[]byte("RIFF"), le(uint32(4+8+len(fmtc)+8+len(data)), 4), []byte("WAVEfmt "), le(uint32(len(fmtc)), 4), fmtc, []byte("data"), le(uint32(len(data)), 4), data}, nil)
	}()
	for _, n := range []string{"01.wav", "02.wav"} {
		if err := os.WriteFile(filepath.Join(dir, n), wav, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := New(cfg, pass, "test")
	defer c.Close()
	var a struct {
		Token  string `json:"token"`
		Tracks []struct {
			Duration float64 `json:"duration"`
		} `json:"tracks"`
	}
	if err := c.AgentJSON(ctx(t), "audio", agent.Request{Path: dir}, &a); err != nil {
		t.Fatal(err)
	}
	if a.Token != "WAV 24bit-48kHz" || len(a.Tracks) != 2 || a.Tracks[0].Duration < 0.99 || a.Tracks[0].Duration > 1.01 {
		t.Fatalf("%+v", a)
	}
}
