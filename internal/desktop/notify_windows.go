//go:build windows

package desktop

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// HasNotify : notifications de bureau disponibles.
const HasNotify = true

// HasAutostart : lancement avec la session possible.
const HasAutostart = true

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	pEnumWindows     = user32.NewProc("EnumWindows")
	pGetClassName    = user32.NewProc("GetClassNameW")
	pGetWindowThread = user32.NewProc("GetWindowThreadProcessId")
	pShellNotifyIcon = syscall.NewLazyDLL("shell32.dll").NewProc("Shell_NotifyIconW")
)

// notifyIconData : NOTIFYICONDATAW (976 octets en 64 bits).
type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Timeout         uint32 // union avec uVersion
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        [16]byte
	BalloonIcon     uintptr
}

var (
	trayMu    sync.Mutex
	trayFound uintptr
	// Une seule fonction de rappel pour tout D3X : Go n'en crée qu'un nombre
	// limité par processus, jamais libérées. Avant, chaque notification en
	// créait une, et D3X se serait arrêté après un millier de notifications.
	trayCallback = sync.OnceValue(func() uintptr {
		pid := uint32(os.Getpid())
		return syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
			var owner uint32
			pGetWindowThread.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
			if owner != pid {
				return 1
			}
			var name [64]uint16
			pGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
			if syscall.UTF16ToString(name[:]) == "SystrayClass" {
				trayFound = hwnd
				return 0
			}
			return 1
		})
	})
)

// trayWindow : la fenêtre cachée de l'icône de D3X (celle de fyne.io/systray,
// classe « SystrayClass », dans ce processus).
func trayWindow() uintptr {
	trayMu.Lock()
	defer trayMu.Unlock()
	trayFound = 0
	pEnumWindows.Call(trayCallback(), 0)
	return trayFound
}

func fill(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}

// Notify montre une notification de D3X, attachée à son icône (Windows la
// range dans le centre de notifications).
func Notify(title, text string) error {
	hwnd := trayWindow()
	if hwnd == 0 {
		return errors.New("icône de D3X absente")
	}
	const (
		nimModify = 0x1
		nifInfo   = 0x10
		niifUser  = 0x4
	)
	var nid notifyIconData
	nid.Size = uint32(unsafe.Sizeof(nid))
	nid.Wnd = hwnd
	nid.ID = 100 // celui que pose fyne.io/systray
	nid.Flags = nifInfo
	nid.InfoFlags = niifUser
	fill(nid.InfoTitle[:], title)
	fill(nid.Info[:], text)
	if r, _, err := pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return err
	}
	return nil
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// Autostart : D3X se lance-t-il avec la session ?
func Autostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("D3X")
	return err == nil
}

// SetAutostart lance (ou non) D3X avec la session, dans la zone de
// notification, sans ouvrir sa fenêtre.
func SetAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("D3X"); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("D3X", `"`+exe+`" -no-browser`)
}
