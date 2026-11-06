//go:build !windows

package slicer

import (
	"os/exec"
	"syscall"
)

func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

type treeKiller struct{ pid int }

func attach(cmd *exec.Cmd) *treeKiller { return &treeKiller{pid: cmd.Process.Pid} }

// kill envoie SIGKILL au groupe de processus (le processus et ses enfants).
func (k *treeKiller) kill() { _ = syscall.Kill(-k.pid, syscall.SIGKILL) }

func (k *treeKiller) close() {}
