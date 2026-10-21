// Package update installe les nouvelles versions de D3X, signées.
//
// Une version n'est jamais installée sans deux preuves : la liste des
// empreintes (checksums.txt) doit être signée par la clé de V3X (ed25519,
// clé publique embarquée dans le binaire), et le fichier téléchargé doit avoir
// l'empreinte SHA-256 annoncée dans cette liste. Sinon, rien ne bouge.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo est le dépôt des versions publiées.
const Repo = "V3x0rV3x/D3X"

// Release est une version publiée.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"` // page de la version
	assets  map[string]string
}

// AssetName est le nom du binaire de cette plateforme dans une version.
func AssetName() string {
	n := "d3x_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

var hc = &http.Client{Timeout: 10 * time.Minute}

// Latest lit la dernière version publiée (ni brouillon ni préversion).
func Latest(ctx context.Context, current string) (*Release, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "D3X/"+current)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub : HTTP %d", resp.StatusCode)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		URL    string `json:"html_url"`
		Pre    bool   `json:"prerelease"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.Pre || rel.Draft {
		return nil, errors.New("pas de version stable")
	}
	r := &Release{Version: strings.TrimPrefix(rel.Tag, "v"), URL: rel.URL, assets: map[string]string{}}
	for _, a := range rel.Assets {
		// Seuls les fichiers servis par GitHub pour CE dépôt sont acceptés.
		if strings.HasPrefix(a.URL, "https://github.com/"+Repo+"/releases/download/") {
			r.assets[a.Name] = a.URL
		}
	}
	return r, nil
}

// Newer dit si a est plus récente que b (« x.y.z », préfixe v toléré).
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

func get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "D3X")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("téléchargement : HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("fichier trop gros")
	}
	return b, nil
}

// PublicKey lit une clé publique ed25519 au format PEM.
func PublicKey(pemBytes []byte) (ed25519.PublicKey, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		return nil, errors.New("clé publique illisible")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("la clé publique n'est pas ed25519")
	}
	return pub, nil
}

// ErrSignature : la liste des empreintes n'est pas signée par V3X.
var ErrSignature = errors.New("signature invalide : cette version n'a pas été publiée par V3X, elle est ignorée")

// Verify contrôle la liste signée et renvoie l'empreinte attendue d'un fichier.
func Verify(pub ed25519.PublicKey, checksums, sig []byte, name string) (string, error) {
	if !ed25519.Verify(pub, checksums, sig) {
		return "", ErrSignature
	}
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s absent de la liste signée", name)
}

// VerifyRelease contrôle la fiche signée de la version (release.txt) :
// « D3X <version> » puis l'empreinte de checksums.txt. Une liste d'empreintes
// signée pour une version ne peut pas en installer une autre (ni revenir à
// une ancienne version sous un nouveau numéro).
func VerifyRelease(pub ed25519.PublicKey, version string, checksums, release, sig []byte) error {
	if !ed25519.Verify(pub, release, sig) {
		return ErrSignature
	}
	sum := sha256.Sum256(checksums)
	want := "D3X " + strings.TrimPrefix(version, "v") + "\n" + hex.EncodeToString(sum[:]) + "\n"
	if string(release) != want {
		return errors.New("la version signée ne correspond pas à celle annoncée : ignorée")
	}
	return nil
}

// Stage télécharge et vérifie la version, puis la range à côté du binaire
// (« d3x.exe.new ») : elle sera installée au prochain redémarrage.
func Stage(ctx context.Context, pub ed25519.PublicKey, r *Release) (string, error) {
	name := AssetName()
	binURL, sumURL, sigURL := r.assets[name], r.assets["checksums.txt"], r.assets["checksums.txt.sig"]
	relURL, relSigURL := r.assets["release.txt"], r.assets["release.txt.sig"]
	if binURL == "" || sumURL == "" || sigURL == "" || relURL == "" || relSigURL == "" {
		return "", errors.New("version incomplète (binaire ou signature manquante)")
	}
	sums, err := get(ctx, sumURL, 1<<20)
	if err != nil {
		return "", err
	}
	sig, err := get(ctx, sigURL, 1<<10)
	if err != nil {
		return "", err
	}
	want, err := Verify(pub, sums, sig, name)
	if err != nil {
		return "", err
	}
	rel, err := get(ctx, relURL, 1<<10)
	if err != nil {
		return "", err
	}
	relSig, err := get(ctx, relSigURL, 1<<10)
	if err != nil {
		return "", err
	}
	if err := VerifyRelease(pub, r.Version, sums, rel, relSig); err != nil {
		return "", err
	}
	bin, err := get(ctx, binURL, 200<<20)
	if err != nil {
		return "", err
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return "", errors.New("empreinte du fichier téléchargé différente de la liste signée : ignoré")
	}
	staged, err := StagedPath()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(staged, bin, 0o755); err != nil {
		return "", fmt.Errorf("impossible d'écrire la mise à jour à côté de D3X : %w", err)
	}
	return staged, nil
}

// ErrNoStaged : aucune mise à jour prête à côté du binaire.
var ErrNoStaged = errors.New("aucune mise à jour prête à côté de D3X")

// exePath : le binaire de D3X, liens symboliques résolus. Le même chemin pour
// préparer, installer et nettoyer une mise à jour (Stage et Swap ne le
// calculaient pas pareil : avec un lien dans le chemin, la mise à jour prête
// restait introuvable au moment de l'installer, retour de Nayr 21/10/26).
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// StagedPath : où la mise à jour vérifiée attend d'être installée.
func StagedPath() (string, error) {
	exe, err := exePath()
	if err != nil {
		return "", err
	}
	return exe + ".new", nil
}

// Staged : une mise à jour vérifiée attend-elle d'être installée ?
func Staged() bool {
	p, err := StagedPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Swap remplace le binaire par la version préparée. Sous Windows, le binaire
// en cours ne peut pas être écrasé mais peut être renommé : il devient
// « .old » et sera effacé au lancement suivant.
func Swap() (bool, error) {
	exe, err := exePath()
	if err != nil {
		return false, err
	}
	staged := exe + ".new"
	if _, err := os.Stat(staged); err != nil {
		return false, fmt.Errorf("%w (%s)", ErrNoStaged, staged)
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return false, err
	}
	if err := os.Rename(staged, exe); err != nil {
		_ = os.Rename(old, exe) // on remet l'ancien
		return false, err
	}
	return true, nil
}

// Cleanup efface l'ancien binaire laissé par une mise à jour.
func Cleanup() {
	if exe, err := exePath(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}
