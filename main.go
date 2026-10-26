// Baratie : publication assistée vers un tracker UNIT3D.
//
// Un serveur web qui tourne sur la machine des fichiers (NAS, Docker,
// seedbox, PC) : la page prépare la fiche, publie sur un clic et met la
// release en seed dans le client torrent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"baratie/internal/config"
	"baratie/internal/server"
)

// version est posée à la compilation (-ldflags "-X main.version=1.2.3").
var version = "dev"

const defaultListen = "127.0.0.1:8769"

func main() {
	// Docker : vérification de santé (l'image n'a pas forcément curl).
	if len(os.Args) > 1 && os.Args[1] == "health" {
		os.Exit(health())
	}
	def := os.Getenv("BARATIE_LISTEN")
	if def == "" {
		def = defaultListen
	}
	listen := flag.String("listen", def, "adresse d'écoute (0.0.0.0:8769 pour ouvrir la page depuis une autre machine)")
	showVersion := flag.Bool("version", false, "afficher la version")
	flag.Parse()
	if *showVersion {
		fmt.Println("Baratie", version)
		return
	}
	setupLog()
	if err := serve(*listen); err != nil {
		log.Println("arrêt :", err)
		os.Exit(1)
	}
}

// setupLog écrit le journal dans le dossier de Baratie et dans le terminal.
func setupLog() {
	p, err := config.Path("baratie.log")
	if err != nil {
		return
	}
	if st, err := os.Stat(p); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f, os.Stdout))
	log.SetFlags(log.LstdFlags)
}

// loopback : l'adresse n'écoute que sur cette machine.
func loopback(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

// serve : la page s'ouvre avec un lien à jeton, stable d'un lancement à
// l'autre (rangé dans le dossier de Baratie, ou donné par BARATIE_TOKEN).
func serve(addr string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration illisible : %w", err)
	}
	token, err := serverToken()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	remote := !loopback(addr)
	srv := server.New(version, cfg, token, port, server.Options{Remote: remote})
	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Baratie %s sur %s", version, ln.Addr())
	if remote {
		log.Printf("Ouvre http://ADRESSE-DE-CETTE-MACHINE:%d/auth?t=%s", port, token)
	} else {
		log.Printf("Ouvre http://127.0.0.1:%d/auth?t=%s", port, token)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case <-stop:
		// Arrêt demandé : on laisse finir ce qui est en cours (dans la limite
		// du délai de Docker).
		for i := 0; i < 8 && srv.Busy(); i++ {
			time.Sleep(time.Second)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return httpSrv.Shutdown(ctx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// serverToken : BARATIE_TOKEN, sinon le jeton rangé (créé au premier lancement).
func serverToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("BARATIE_TOKEN")); t != "" {
		if len(t) < 24 {
			return "", errors.New("BARATIE_TOKEN trop court (24 caractères au moins)")
		}
		return t, nil
	}
	p, err := config.Path("token")
	if err != nil {
		return "", err
	}
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) >= 24 {
		return strings.TrimSpace(string(b)), nil
	}
	t := server.NewToken()
	if err := config.WriteFileAtomic("token", []byte(t)); err != nil {
		return "", err
	}
	return t, nil
}

// health interroge le Baratie local (HEALTHCHECK de l'image Docker).
func health() int {
	port := "8769"
	if a := os.Getenv("BARATIE_LISTEN"); a != "" {
		if _, p, err := net.SplitHostPort(a); err == nil && p != "" && p != "0" {
			if _, err := strconv.Atoi(p); err == nil {
				port = p
			}
		}
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16))
	if string(b) != "Baratie" {
		return 1
	}
	return 0
}
