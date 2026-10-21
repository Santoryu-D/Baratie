package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("binaire de test")
	sum := sha256.Sum256(bin)
	list := []byte(hex.EncodeToString(sum[:]) + "  " + AssetName() + "\n0000000000000000000000000000000000000000000000000000000000000000  autre\n")
	sig := ed25519.Sign(priv, list)

	got, err := Verify(pub, list, sig, AssetName())
	if err != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatalf("liste valide refusée : %v", err)
	}
	// Liste modifiée après signature : refusée.
	bad := append([]byte{}, list...)
	bad[0] ^= 1
	if _, err := Verify(pub, bad, sig, AssetName()); err != ErrSignature {
		t.Fatalf("liste falsifiée acceptée : %v", err)
	}
	// Autre clé : refusée.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(other, list, sig, AssetName()); err != ErrSignature {
		t.Fatal("signature d'une autre clé acceptée")
	}
	if _, err := Verify(pub, list, sig, "absent"); err == nil {
		t.Fatal("fichier absent de la liste accepté")
	}
}

func TestPublicKeyFromRepo(t *testing.T) {
	b, err := os.ReadFile("../../d3x-signature.pub")
	if err != nil {
		t.Skip("clé publique du dépôt absente")
	}
	if _, err := PublicKey(b); err != nil {
		t.Fatalf("clé publique du dépôt illisible : %v", err)
	}
	// Une clé RSA n'est pas acceptée.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	if _, err := PublicKey(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})); err != nil {
		t.Fatal(err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.5.0", "0.4.0", true}, {"v0.4.1", "0.4.0", true}, {"0.4.0", "0.4.0", false}, {"0.10.0", "0.9.9", true}, {"0.4.0", "dev", true}} {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
}

// D3X_RELEASE_DIR=dossier : vérifie une vraie version téléchargée (binaire,
// checksums.txt et checksums.txt.sig produits par la CI) avec la clé du dépôt.
func TestRealRelease(t *testing.T) {
	dir := os.Getenv("D3X_RELEASE_DIR")
	if dir == "" {
		t.Skip("D3X_RELEASE_DIR non défini")
	}
	pemBytes, _ := os.ReadFile("../../d3x-signature.pub")
	pub, err := PublicKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	sums, _ := os.ReadFile(dir + "/checksums.txt")
	sig, _ := os.ReadFile(dir + "/checksums.txt.sig")
	want, err := Verify(pub, sums, sig, "d3x_windows_amd64.exe")
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := os.ReadFile(dir + "/d3x_windows_amd64.exe")
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		t.Fatal("empreinte différente")
	}
}

func TestVerifyRelease(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sums := []byte("abc  d3x_linux_amd64\n")
	h := sha256.Sum256(sums)
	rel := []byte("D3X 0.9.1\n" + hex.EncodeToString(h[:]) + "\n")
	sig := ed25519.Sign(priv, rel)
	if err := VerifyRelease(pub, "v0.9.1", sums, rel, sig); err != nil {
		t.Fatal(err)
	}
	// Une ancienne version signée présentée sous un nouveau numéro : refusée.
	if err := VerifyRelease(pub, "0.9.5", sums, rel, sig); err == nil {
		t.Fatal("numéro de version différent accepté")
	}
	// Autre liste d'empreintes : refusée.
	if err := VerifyRelease(pub, "0.9.1", []byte("autre"), rel, sig); err == nil {
		t.Fatal("liste d'empreintes différente acceptée")
	}
	// Signature fausse : refusée.
	if err := VerifyRelease(pub, "0.9.1", sums, rel, make([]byte, 64)); err != ErrSignature {
		t.Fatalf("signature : %v", err)
	}
}

// Pas de mise à jour prête : Swap ne touche à rien et le dit (au lieu d'un
// « mise à jour impossible : <nil> » muet, retour de Nayr 21/10/26), avec le
// même chemin que celui où Stage l'aurait posée.
func TestSwapWithoutStaged(t *testing.T) {
	p, err := StagedPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Skip("une mise à jour traîne à côté du binaire de test")
	}
	if Staged() {
		t.Fatal("Staged sans fichier")
	}
	swapped, err := Swap()
	if swapped || !errors.Is(err, ErrNoStaged) {
		t.Fatalf("Swap = %v, %v ; attendu false, ErrNoStaged", swapped, err)
	}
	if !strings.Contains(err.Error(), p) {
		t.Fatalf("le chemin cherché manque dans l'erreur : %v", err)
	}
}
