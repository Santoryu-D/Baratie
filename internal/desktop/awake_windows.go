//go:build windows

package desktop

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Demande d'alimentation (comme un lecteur vidéo) : tant qu'elle est posée,
// Windows ne met pas l'ordinateur en veille au bout de son délai d'inactivité.
// L'écran peut s'éteindre ; une veille demandée (bouton, capot) passe quand
// même. Elle tombe d'elle-même quand D3X s'arrête.
var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	pPowerCreateRequest = kernel32.NewProc("PowerCreateRequest")
	pPowerSetRequest    = kernel32.NewProc("PowerSetRequest")
	pPowerClearRequest  = kernel32.NewProc("PowerClearRequest")
)

const (
	powerRequestContextSimpleString = 0x1
	powerRequestSystemRequired      = 1
)

// reasonContext : REASON_CONTEXT, forme « texte simple » (l'union occupe la
// place de sa forme détaillée).
type reasonContext struct {
	version uint32
	flags   uint32
	reason  *uint16
	_       [16]byte
}

var awake struct {
	sync.Mutex
	h      uintptr
	on     bool
	reason *uint16 // gardé en vie avec la demande
}

// KeepAwake pose (on) ou lève la demande. Rend vrai si l'état a changé.
func KeepAwake(on bool) bool {
	awake.Lock()
	defer awake.Unlock()
	if on == awake.on {
		return false
	}
	if awake.h == 0 {
		if !on {
			return false
		}
		if err := pPowerCreateRequest.Find(); err != nil {
			return false
		}
		reason, err := syscall.UTF16PtrFromString("D3X : lot de publications en cours")
		if err != nil {
			return false
		}
		rc := reasonContext{flags: powerRequestContextSimpleString, reason: reason}
		h, _, _ := pPowerCreateRequest.Call(uintptr(unsafe.Pointer(&rc)))
		if h == 0 || h == uintptr(windows.InvalidHandle) {
			return false
		}
		awake.h, awake.reason = h, reason
	}
	proc := pPowerClearRequest
	if on {
		proc = pPowerSetRequest
	}
	if r, _, _ := proc.Call(awake.h, powerRequestSystemRequired); r == 0 {
		return false
	}
	awake.on = on
	return true
}
