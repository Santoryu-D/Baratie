// Package sshtest est une seedbox en mémoire pour les tests et le mode démo :
// un vrai serveur SSH qui comprend les commandes de D3X et lance l'agent dans
// le processus (sur les fichiers de cette machine).
package sshtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/V3x0rV3x/D3X/internal/agent"
)

// User est le seul utilisateur accepté.
const User = "membre"

// Box est une seedbox en mémoire : un vrai serveur SSH (x/crypto/ssh)
// qui comprend les quelques commandes que D3X envoie, et lance l'agent dans
// le processus du test.
type Box struct {
	Addr    string
	HostKey ssh.Signer

	mu      sync.Mutex
	pass    string
	userKey ssh.PublicKey
	files   map[string][]byte // fichiers déposés ($HOME/.d3x/…)
	uploads int
	cmds    []string
}

func New(t testing.TB, pass string) *Box {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hk, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	b := &Box{HostKey: hk, pass: pass, files: map[string][]byte{}}
	conf := &ssh.ServerConfig{
		PasswordCallback: func(m ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			b.mu.Lock()
			ok := m.User() == User && string(p) == b.pass
			b.mu.Unlock()
			if ok {
				return nil, nil
			}
			return nil, errors.New("refusé")
		},
		PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			b.mu.Lock()
			ok := m.User() == User && b.userKey != nil && bytes.Equal(k.Marshal(), b.userKey.Marshal())
			b.mu.Unlock()
			if ok {
				return nil, nil
			}
			return nil, errors.New("refusé")
		},
	}
	conf.AddHostKey(hk)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	b.Addr = ln.Addr().String()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(c, conf)
		}
	}()
	return b
}

func (b *Box) serve(c net.Conn, conf *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, conf)
	if err != nil {
		c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "non")
			continue
		}
		ch, in, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for r := range in {
				if r.Type != "exec" {
					_ = r.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				_ = ssh.Unmarshal(r.Payload, &p)
				_ = r.Reply(true, nil)
				go func() {
					st := b.run(p.Command, ch, ch, ch.Stderr())
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{st}))
					ch.Close()
				}()
			}
		}()
	}
}

// quoted renvoie la n-ième chaîne entre guillemets d'une commande.
func quoted(cmd string, n int) string {
	parts := strings.Split(cmd, `"`)
	if 2*n+1 >= len(parts) {
		return ""
	}
	return parts[2*n+1]
}

func (b *Box) run(cmd string, stdin io.Reader, stdout, stderr io.Writer) uint32 {
	b.mu.Lock()
	b.cmds = append(b.cmds, cmd)
	b.mu.Unlock()
	switch {
	case cmd == "uname -sm":
		io.WriteString(stdout, "Linux x86_64\n")
	case cmd == "uname -m":
		io.WriteString(stdout, "x86_64\n")
	case strings.HasPrefix(cmd, `cat "`):
		b.mu.Lock()
		f, ok := b.files[quoted(cmd, 0)]
		b.mu.Unlock()
		if !ok {
			return 1
		}
		stdout.Write(f)
	case strings.HasPrefix(cmd, "umask 077;"):
		data, _ := io.ReadAll(stdin)
		parts := strings.Split(cmd, `"`)
		b.mu.Lock()
		b.files[parts[len(parts)-2]] = data
		b.uploads++
		b.mu.Unlock()
	case strings.HasPrefix(cmd, "find "):
	case strings.HasPrefix(cmd, `"$HOME/.d3x/d3x-agent-`):
		p := quoted(cmd, 0)
		b.mu.Lock()
		_, ok := b.files[p]
		b.mu.Unlock()
		if !ok {
			io.WriteString(stderr, "introuvable\n")
			return 127
		}
		args := strings.Fields(cmd[len(p)+2:])
		if len(args) == 0 || args[0] != "agent" {
			return 2
		}
		if err := agent.Run("test", args[1:], stdout, stderr); err != nil {
			io.WriteString(stderr, "erreur : "+err.Error()+"\n")
			return 1
		}
	default:
		io.WriteString(stderr, "commande inattendue\n")
		return 2
	}
	return 0
}

// Host et Port de la seedbox.
func (b *Box) Host() string { h, _, _ := net.SplitHostPort(b.Addr); return h }

func (b *Box) Port() int {
	_, p, _ := net.SplitHostPort(b.Addr)
	n, _ := strconv.Atoi(p)
	return n
}

// HostLine est la clé d'hôte au format authorized_keys.
func (b *Box) HostLine() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(b.HostKey.PublicKey())))
}

// SetPass change le mot de passe accepté.
func (b *Box) SetPass(p string) { b.mu.Lock(); b.pass = p; b.mu.Unlock() }

// SetUserKey accepte cette clé publique.
func (b *Box) SetUserKey(k ssh.PublicKey) { b.mu.Lock(); b.userKey = k; b.mu.Unlock() }

// Uploads : nombre de dépôts de l'agent.
func (b *Box) Uploads() int { b.mu.Lock(); defer b.mu.Unlock(); return b.uploads }

// Commands : commandes reçues.
func (b *Box) Commands() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.cmds...)
}
