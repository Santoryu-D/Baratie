//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/V3x0rV3x/D3X/internal/update"
	"golang.org/x/sys/windows"
)

// Les structures passées à Windows ont la taille de leurs modèles C : une
// erreur ici ferait écrire Windows à côté.
func TestTaskbarLayout(t *testing.T) {
	if n := unsafe.Sizeof(propertyKey{}); n != 20 {
		t.Fatalf("PROPERTYKEY : %d octets au lieu de 20", n)
	}
	want := uintptr(24)
	if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
		want = 16
	}
	if n := unsafe.Sizeof(propVariant{}); n != want {
		t.Fatalf("PROPVARIANT : %d octets au lieu de %d", n, want)
	}
}

// Essai réel (D3X_TEST_EDGE=1) : la fenêtre ouverte porte l'identité de D3X
// (relue sur la fenêtre elle-même), relance comprise.
func TestTaskbarIdentityRealBrowser(t *testing.T) {
	if os.Getenv("D3X_TEST_EDGE") != "1" {
		t.Skip("essai réel : D3X_TEST_EDGE=1")
	}
	dir := filepath.Join(t.TempDir(), "fenetre-essai")
	w := OpenWindow("about:blank", dir)
	if w == nil {
		t.Skip("aucun navigateur")
	}
	defer CloseWindow(dir)
	w.WaitIdentity()
	exe, _ := update.ExePath()
	hs := appWindows(strings.ToLower(dir), map[uint32]bool{})
	if len(hs) == 0 {
		t.Fatal("aucune fenêtre trouvée")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}
	for _, h := range hs {
		ps := windowStore(h)
		if ps == nil {
			t.Fatal("propriétés de la fenêtre illisibles")
		}
		got := map[string]string{
			"ID":      ps.getString(&keyAppID),
			"command": ps.getString(&keyRelaunchCommand),
			"icon":    ps.getString(&keyRelaunchIcon),
			"name":    ps.getString(&keyRelaunchName),
		}
		ps.release()
		if got["ID"] != appID || got["command"] != `"`+exe+`"` || got["icon"] != exe+",0" || got["name"] != "D3X" {
			t.Fatalf("fenêtre %#x : %v", h, got)
		}
		t.Logf("fenêtre %#x : %v", h, got)
	}
}
