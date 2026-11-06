//go:build windows

package slicer

import (
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	createNoWindow         = 0x08000000
	createNewProcessGroup  = 0x00000200
	jobObjectExtendedLimit = 9
	jobLimitKillOnClose    = 0x2000
	processSetQuota        = 0x0100
	processTerminate       = 0x0001
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject    = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJob  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject = kernel32.NewProc("TerminateJobObject")
)

type ioCounters struct{ a, b, c, d, e, f uint64 }

type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimit struct {
	Basic                 jobBasicLimit
	IO                    ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// prepare configure la création du processus : pas de fenêtre console, groupe
// de processus propre.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup,
	}
}

// treeKiller arrête un processus et tous ses descendants.
type treeKiller struct {
	pid int
	job syscall.Handle
}

// attach place le processus dans un job object « kill on close » ; à défaut
// (échec de l'API) kill() retombe sur taskkill /T.
func attach(cmd *exec.Cmd) *treeKiller {
	k := &treeKiller{pid: cmd.Process.Pid}
	job, _, _ := procCreateJobObject.Call(0, 0)
	if job == 0 {
		return k
	}
	var info jobExtendedLimit
	info.Basic.LimitFlags = jobLimitKillOnClose
	r, _, _ := procSetInformationJob.Call(job, jobObjectExtendedLimit, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return k
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid))
	if err != nil {
		syscall.CloseHandle(syscall.Handle(job))
		return k
	}
	defer syscall.CloseHandle(h)
	r, _, _ = procAssignProcessToJob.Call(job, uintptr(h))
	if r == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return k
	}
	k.job = syscall.Handle(job)
	return k
}

func (k *treeKiller) kill() {
	if k.job != 0 {
		procTerminateJobObject.Call(uintptr(k.job), 1)
		return
	}
	// repli : taskkill /T arrête l'arbre
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(k.pid)).Run()
}

func (k *treeKiller) close() {
	if k.job != 0 {
		syscall.CloseHandle(k.job)
		k.job = 0
	}
}
