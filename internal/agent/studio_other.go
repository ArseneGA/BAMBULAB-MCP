//go:build !windows

package agent

import "os/exec"

func studioProcesses() []StudioProcess { return nil }
func topWindows() []StudioWindow       { return nil }
func detach(*exec.Cmd)                 {}

func processCommandLine(int) (string, error) { return "", nil }

// CloseWindowsOfPID : sans objet hors Windows.
func CloseWindowsOfPID(int) int { return 0 }
