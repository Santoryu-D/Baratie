//go:build windows

package desktop

import (
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processCommandLineInformation : classe de NtQueryInformationProcess qui rend
// la ligne de commande d'un processus (Windows 8.1 et plus).
const processCommandLineInformation = 60

// CloseWindow ferme la fenêtre d'application de D3X en arrêtant les processus
// du navigateur lancés avec SON profil (profileDir), et eux seuls : un Edge ou
// un Chrome ouvert par ailleurs n'est jamais touché. Sans ça, la fenêtre
// survivait parfois à D3X, en rectangle blanc ou gris impossible à fermer
// (retours de V3x0r et de CoprinusComatus, 20/10/26). Rend le nombre de
// processus arrêtés, une fois qu'ils sont vraiment partis (3 s au plus) : une
// fenêtre rouverte aussitôt sur le même profil se raccrochait sinon au
// navigateur mourant et restait vide (Luci, 21/10/26).
func CloseWindow(profileDir string) int {
	want := strings.ToLower(profileDir)
	if want == "" {
		return 0
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var stopped []windows.Handle
	defer func() {
		for _, h := range stopped {
			_ = windows.CloseHandle(h)
		}
	}()
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		name := strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
		if name != "msedge.exe" && name != "chrome.exe" {
			continue
		}
		cmd, ok := commandLine(e.ProcessID)
		if !ok {
			continue
		}
		cmd = strings.ToLower(cmd)
		if !strings.Contains(cmd, "--user-data-dir") || !strings.Contains(cmd, want) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, e.ProcessID)
		if err != nil {
			continue
		}
		if windows.TerminateProcess(h, 0) != nil {
			_ = windows.CloseHandle(h)
			continue
		}
		stopped = append(stopped, h)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, h := range stopped {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		_, _ = windows.WaitForSingleObject(h, uint32(left/time.Millisecond))
	}
	return len(stopped)
}

// commandLine lit la ligne de commande d'un processus.
func commandLine(pid uint32) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, 16<<10)
	var n uint32
	if err := windows.NtQueryInformationProcess(h, processCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n); err != nil {
		if n <= uint32(len(buf)) || n > 1<<20 {
			return "", false
		}
		buf = make([]byte, n)
		if err := windows.NtQueryInformationProcess(h, processCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n); err != nil {
			return "", false
		}
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	if us.Buffer == nil || us.Length == 0 {
		return "", false
	}
	return windows.UTF16ToString(unsafe.Slice(us.Buffer, int(us.Length)/2)), true
}
