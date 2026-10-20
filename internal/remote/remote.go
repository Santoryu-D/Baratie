// Package remote parle à une seedbox par SSH. D3X y dépose son agent (lui-même,
// compilé pour Linux) et lui fait lire et hacher les fichiers sur place : seul
// le résultat (liste, .torrent, morceau de fichier) voyage.
//
// Sécurité : clé d'hôte vérifiée (acceptée une fois sur son empreinte, tout
// changement bloque), mot de passe ou phrase de passe gardés en mémoire
// seulement, agent vérifié par son empreinte SHA-256 avant usage.
package remote

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/V3x0rV3x/D3X/internal/agent"
	"github.com/V3x0rV3x/D3X/internal/config"
)

// Agents Linux embarqués à la compilation (d3x-agent-linux-amd64.gz…).
//
//go:embed agents
var agentsFS embed.FS

// Config : la seedbox (sans secret), telle qu'enregistrée dans les réglages.
type Config = config.SSH

func addr(c Config) string {
	p := c.Port
	if p == 0 {
		p = 22
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(p))
}

// HostKeyError : clé d'hôte inconnue (première connexion) ou CHANGÉE.
type HostKeyError struct {
	Fingerprint string
	Key         string
	Changed     bool
}

func (e *HostKeyError) Error() string {
	if e.Changed {
		return "la clé d'hôte de la seedbox a CHANGÉ (" + e.Fingerprint + ") : vérifie auprès de ton hébergeur avant d'accepter la nouvelle"
	}
	return "seedbox inconnue : vérifie son empreinte (" + e.Fingerprint + ") avant de l'accepter"
}

// ErrNoAuth : ni clé utilisable ni mot de passe (à redonner après un
// redémarrage de D3X, le mot de passe n'étant jamais enregistré).
var ErrNoAuth = errors.New("aucune clé SSH utilisable et pas de mot de passe : indique ta clé ou ton mot de passe")

// Client est une connexion à la seedbox.
type Client struct {
	cfg     Config
	secret  string // mot de passe ou phrase de passe de la clé : en mémoire seulement
	version string

	mu        sync.Mutex
	conn      *ssh.Client
	agentPath string
}

// New prépare un client (la connexion se fait au premier usage).
func New(cfg Config, secret, version string) *Client {
	return &Client{cfg: cfg, secret: secret, version: version}
}

// Config renvoie la configuration du client.
func (c *Client) Config() Config { return c.cfg }

func (c *Client) authMethods() []ssh.AuthMethod {
	var signers []ssh.Signer
	paths := []string{c.cfg.KeyPath}
	if c.cfg.KeyPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			for _, n := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
				paths = append(paths, filepath.Join(home, ".ssh", n))
			}
		}
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s, err := ssh.ParsePrivateKey(b)
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) && c.secret != "" {
			s, err = ssh.ParsePrivateKeyWithPassphrase(b, []byte(c.secret))
		}
		if err == nil {
			signers = append(signers, s)
		}
	}
	var out []ssh.AuthMethod
	if len(signers) > 0 {
		out = append(out, ssh.PublicKeys(signers...))
	}
	if c.secret != "" {
		// Questions du serveur : seul le mot de passe est donné (jamais à un
		// code à usage unique ou à une autre question).
		out = append(out, ssh.Password(c.secret), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i, q := range qs {
				l := strings.ToLower(q)
				if !strings.Contains(l, "pass") && !strings.Contains(l, "mot de passe") {
					return nil, errors.New("la seedbox demande autre chose que le mot de passe (« " + strings.TrimSpace(q) + " ») : connexion par clé SSH conseillée")
				}
				ans[i] = c.secret
			}
			return ans, nil
		}))
	}
	return out
}

func (c *Client) dial() (*ssh.Client, error) {
	auths := c.authMethods()
	if len(auths) == 0 {
		return nil, ErrNoAuth
	}
	conf := &ssh.ClientConfig{
		User:    c.cfg.User,
		Auth:    auths,
		Timeout: 15 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			fp := ssh.FingerprintSHA256(key)
			switch {
			case c.cfg.HostKey == "":
				return &HostKeyError{Fingerprint: fp, Key: line}
			case strings.TrimSpace(c.cfg.HostKey) != line:
				return &HostKeyError{Fingerprint: fp, Key: line, Changed: true}
			}
			return nil
		},
	}
	conn, err := ssh.Dial("tcp", addr(c.cfg), conf)
	if err != nil {
		var hk *HostKeyError
		if errors.As(err, &hk) {
			return nil, hk
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, errors.New("la seedbox refuse la connexion : clé ou mot de passe refusé")
		}
		return nil, fmt.Errorf("seedbox injoignable : %w", err)
	}
	// Garde la connexion en vie ; une connexion morte est refaite au besoin.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			if _, _, err := conn.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				conn.Close()
				c.mu.Lock()
				if c.conn == conn {
					c.conn = nil
				}
				c.mu.Unlock()
				return
			}
		}
	}()
	return conn, nil
}

func (c *Client) session() (*ssh.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if c.conn == nil {
			conn, err := c.dial()
			if err != nil {
				return nil, err
			}
			c.conn = conn
		}
		s, err := c.conn.NewSession()
		if err == nil {
			return s, nil
		}
		c.conn.Close()
		c.conn = nil
	}
	return nil, errors.New("connexion à la seedbox perdue")
}

// Close ferme la connexion.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// shell lance une commande (sans données du membre dedans : les demandes à
// l'agent passent en base64).
func (c *Client) shell(ctx context.Context, cmd string, stdin io.Reader, onErrLine func(string)) ([]byte, error) {
	s, err := c.session()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var out bytes.Buffer
	s.Stdout = &out
	if stdin != nil {
		s.Stdin = stdin
	}
	errPipe, _ := s.StderrPipe()
	var errLines []string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(errPipe)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if onErrLine != nil && (strings.HasPrefix(line, "P ") || line == "C") {
				onErrLine(line)
				continue
			}
			errLines = append(errLines, line)
		}
	}()
	if err := s.Start(cmd); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case <-ctx.Done():
		_ = s.Signal(ssh.SIGINT)
		s.Close()
		return nil, ctx.Err()
	case err = <-done:
	}
	wg.Wait()
	if err != nil {
		msg := strings.TrimSpace(strings.Join(errLines, " "))
		msg = strings.TrimPrefix(msg, "erreur : ")
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return out.Bytes(), nil
}

// Test se connecte et décrit la machine (« Linux x86_64 »).
func (c *Client) Test(ctx context.Context) (string, error) {
	out, err := c.shell(ctx, "uname -sm", nil, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// agentBinary donne l'agent à déposer (remplacé dans les tests).
var agentBinary = agentFor

// agentFor renvoie l'agent embarqué pour l'architecture distante.
func agentFor(uname string) ([]byte, error) {
	arch := ""
	switch strings.TrimSpace(uname) {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		return nil, fmt.Errorf("seedbox %s non prise en charge (Linux x86_64 ou arm64 seulement)", strings.TrimSpace(uname))
	}
	gz, err := agentsFS.ReadFile("agents/d3x-agent-linux-" + arch + ".gz")
	if err != nil {
		return nil, errors.New("l'agent pour seedbox n'est pas inclus dans cette version de D3X (version de développement)")
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// ensureAgent dépose l'agent dans ~/.d3x (une fois par version) et vérifie
// son empreinte sur la seedbox.
func (c *Client) ensureAgent(ctx context.Context) (string, error) {
	c.mu.Lock()
	p := c.agentPath
	c.mu.Unlock()
	if p != "" {
		return p, nil
	}
	arch, err := c.shell(ctx, "uname -m", nil, nil)
	if err != nil {
		return "", err
	}
	bin, err := agentBinary(string(arch))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bin)
	full := hex.EncodeToString(sum[:])
	path := "$HOME/.d3x/d3x-agent-" + full[:16]
	// L'agent est relu et vérifié ici, octet par octet (sans compter sur un
	// outil de la seedbox).
	check := func() bool {
		out, err := c.shell(ctx, `cat "`+path+`" 2>/dev/null`, nil, nil)
		if err != nil {
			return false
		}
		got := sha256.Sum256(out)
		return hex.EncodeToString(got[:]) == full
	}
	if !check() {
		up := `umask 077; mkdir -p "$HOME/.d3x" && cat > "$HOME/.d3x/.agent.tmp" && chmod 700 "$HOME/.d3x/.agent.tmp" && mv -f "$HOME/.d3x/.agent.tmp" "` + path + `"`
		if _, err := c.shell(ctx, up, bytes.NewReader(bin), nil); err != nil {
			return "", fmt.Errorf("dépôt de l'agent sur la seedbox impossible : %w", err)
		}
		if !check() {
			return "", errors.New("l'agent déposé sur la seedbox n'a pas la bonne empreinte : rien n'est lancé")
		}
		// Les anciennes versions de l'agent sont retirées (dossier de D3X seulement).
		_, _ = c.shell(ctx, `find "$HOME/.d3x" -maxdepth 1 -name 'd3x-agent-*' ! -name 'd3x-agent-`+full[:16]+`' -type f -delete`, nil, nil)
	}
	c.mu.Lock()
	c.agentPath = path
	c.mu.Unlock()
	return path, nil
}

// Agent lance une commande de l'agent. onProgress reçoit l'avancement d'un
// hachage (octets lus, total) et cached signale un .torrent repris du cache.
func (c *Client) Agent(ctx context.Context, cmd string, req agent.Request, onProgress func(done, total int64), cached *bool) ([]byte, error) {
	path, err := c.ensureAgent(ctx)
	if err != nil {
		return nil, err
	}
	line := `"` + path + `" agent ` + cmd + " " + agent.Encode(req)
	return c.shell(ctx, line, nil, func(l string) {
		if l == "C" {
			if cached != nil {
				*cached = true
			}
			return
		}
		f := strings.Fields(l)
		if len(f) == 3 && onProgress != nil {
			d, _ := strconv.ParseInt(f[1], 10, 64)
			t, _ := strconv.ParseInt(f[2], 10, 64)
			onProgress(d, t)
		}
	})
}

// AgentJSON lance une commande et décode sa réponse JSON.
func (c *Client) AgentJSON(ctx context.Context, cmd string, req agent.Request, out interface{}) error {
	b, err := c.Agent(ctx, cmd, req, nil, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
