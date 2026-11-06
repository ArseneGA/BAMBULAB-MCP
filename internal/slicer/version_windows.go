//go:build windows

package slicer

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	versionDLL      = syscall.NewLazyDLL("version.dll")
	procVerSize     = versionDLL.NewProc("GetFileVersionInfoSizeW")
	procVerInfo     = versionDLL.NewProc("GetFileVersionInfoW")
	procVerQueryVal = versionDLL.NewProc("VerQueryValueW")
)

// fileVersion lit la ressource VS_FIXEDFILEINFO de l'exécutable et la formate
// comme Bambu Studio (02.08.02.61).
func fileVersion(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	size, _, _ := procVerSize.Call(uintptr(unsafe.Pointer(p)), 0)
	if size == 0 {
		return ""
	}
	buf := make([]byte, size)
	r, _, _ := procVerInfo.Call(uintptr(unsafe.Pointer(p)), 0, size, uintptr(unsafe.Pointer(&buf[0])))
	if r == 0 {
		return ""
	}
	root, _ := syscall.UTF16PtrFromString(`\`)
	var info unsafe.Pointer
	var n uint32
	r, _, _ = procVerQueryVal.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(root)),
		uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || n < 16 {
		return ""
	}
	// VS_FIXEDFILEINFO : signature, structVersion, fileVersionMS, fileVersionLS
	words := (*[4]uint32)(info)
	ms, ls := words[2], words[3]
	return fmt.Sprintf("%02d.%02d.%02d.%02d", ms>>16, ms&0xffff, ls>>16, ls&0xffff)
}
