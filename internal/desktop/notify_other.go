//go:build !windows

package desktop

import (
	"errors"
	"os/exec"
	"runtime"
)

// HasNotify : notifications de bureau (notify-send sous Linux, Centre de
// notifications sous macOS).
const HasNotify = true

// HasAutostart : le lancement avec la session n'est géré que sous Windows.
const HasAutostart = false

// Notify montre une notification de bureau.
func Notify(title, text string) error {
	switch runtime.GOOS {
	case "darwin":
		// Le texte passe en argument, jamais dans le script.
		return exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, text).Run()
	default:
		p, err := exec.LookPath("notify-send")
		if err != nil {
			return errors.New("notify-send absent")
		}
		return exec.Command(p, "--app-name=D3X", "--", title, text).Run()
	}
}

// Autostart n'existe pas ici.
func Autostart() bool { return false }

// SetAutostart n'existe pas ici.
func SetAutostart(bool) error { return errors.New("lancement avec la session : Windows seulement") }
