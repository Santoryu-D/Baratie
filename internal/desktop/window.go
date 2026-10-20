// Package desktop donne à D3X sa fenêtre d'application : la page dans une
// fenêtre sans barre d'adresse (Edge ou Chrome en mode application, profil à
// part), l'icône de la zone de notification sous Windows et les messages
// d'erreur quand il n'y a pas de console.
package desktop

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Icon est l'icône de D3X (ICO multi-tailles).
//
//go:embed icon.ico
var Icon []byte

// appBrowser trouve un navigateur capable d'ouvrir une fenêtre d'application.
func appBrowser() string {
	var cands []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			if base := os.Getenv(env); base != "" {
				cands = append(cands,
					filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
					filepath.Join(base, `Google\Chrome\Application\chrome.exe`))
			}
		}
	case "darwin":
		cands = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "microsoft-edge"} {
			if p, err := exec.LookPath(n); err == nil {
				cands = append(cands, p)
			}
		}
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// Window est une fenêtre d'application ouverte.
type Window struct{ cmd *exec.Cmd }

// OpenWindow ouvre la page dans une fenêtre d'application, avec un profil à
// part (profileDir) : elle a sa propre icône dans la barre des tâches, et sa
// fermeture se voit (Wait). nil si aucun navigateur ne s'y prête.
func OpenWindow(url, profileDir string) *Window {
	b := appBrowser()
	if b == "" {
		return nil
	}
	_ = os.MkdirAll(profileDir, 0o700)
	cmd := exec.Command(b,
		"--app="+url,
		"--user-data-dir="+profileDir,
		"--window-size=1280,880",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate",
	)
	hideConsole(cmd)
	if err := cmd.Start(); err != nil {
		return nil
	}
	return &Window{cmd: cmd}
}

// Wait attend la fermeture de la fenêtre.
func (w *Window) Wait() { _ = w.cmd.Wait() }

// OpenDefault ouvre la page dans le navigateur par défaut.
func OpenDefault(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	hideConsole(cmd)
	return cmd.Start()
}
