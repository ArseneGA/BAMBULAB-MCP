//go:build windows

package agent

import (
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetWindowTextLen = user32.NewProc("GetWindowTextLengthW")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procPostMessageW     = user32.NewProc("PostMessageW")
)

const wmClose = 0x0010

// studioProcesses énumère les processus bambu-studio.exe (Toolhelp32, lecture seule).
func studioProcesses() []StudioProcess {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []StudioProcess
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(name, "bambu-studio.exe") {
			out = append(out, StudioProcess{PID: int(pe.ProcessID), Exe: name})
		}
	}
	return out
}

type winInfo struct {
	pid     uint32
	hwnd    windows.HWND
	title   string
	visible bool
}

// enumWindows liste les fenêtres de premier niveau (user32 : EnumWindows,
// GetWindowTextW, IsWindowVisible, GetWindowThreadProcessId).
func enumWindows() []winInfo {
	var out []winInfo
	cb := syscall.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		var pid uint32
		_, _ = windows.GetWindowThreadProcessId(h, &pid)
		n, _, _ := procGetWindowTextLen.Call(uintptr(h))
		title := ""
		if n > 0 {
			buf := make([]uint16, n+1)
			procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			title = string(utf16.Decode(buf[:n]))
		}
		vis, _, _ := procIsWindowVisible.Call(uintptr(h))
		out = append(out, winInfo{pid: pid, hwnd: h, title: title, visible: vis != 0})
		return 1
	})
	_ = windows.EnumWindows(cb, nil)
	return out
}

func topWindows() []StudioWindow {
	var out []StudioWindow
	for _, w := range enumWindows() {
		out = append(out, StudioWindow{PID: int(w.pid), Title: w.title, Visible: w.visible})
	}
	return out
}

// CloseWindowsOfPID demande la fermeture propre (WM_CLOSE) des fenêtres d'un
// processus précis, et seulement de celui-là. Renvoie le nombre de fenêtres sollicitées.
func CloseWindowsOfPID(pid int) int {
	n := 0
	for _, w := range enumWindows() {
		if int(w.pid) == pid && w.visible {
			procPostMessageW.Call(uintptr(w.hwnd), wmClose, 0, 0)
			n++
		}
	}
	return n
}

// processCommandLine lit la ligne de commande d'un processus du même utilisateur
// (PEB -> RTL_USER_PROCESS_PARAMETERS, lecture seule de sa mémoire). Sert à retrouver
// le fichier passé à Studio (double-clic dans l'Explorateur, `bambu-studio.exe fichier`).
func processCommandLine(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var pbi windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil); err != nil {
		return "", err
	}
	var peb windows.PEB
	if err := windows.ReadProcessMemory(h, uintptr(unsafe.Pointer(pbi.PebBaseAddress)), (*byte)(unsafe.Pointer(&peb)), unsafe.Sizeof(peb), nil); err != nil {
		return "", err
	}
	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err := windows.ReadProcessMemory(h, uintptr(unsafe.Pointer(peb.ProcessParameters)), (*byte)(unsafe.Pointer(&params)), unsafe.Sizeof(params), nil); err != nil {
		return "", err
	}
	n := int(params.CommandLine.Length) / 2
	if n == 0 || n > 32768 {
		return "", nil
	}
	buf := make([]uint16, n)
	if err := windows.ReadProcessMemory(h, uintptr(unsafe.Pointer(params.CommandLine.Buffer)), (*byte)(unsafe.Pointer(&buf[0])), uintptr(n*2), nil); err != nil {
		return "", err
	}
	return string(utf16.Decode(buf)), nil
}

// detach lance le processus indépendamment de l'agent (nouveau groupe de processus).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | 0x00000008 /* DETACHED_PROCESS */}
}
