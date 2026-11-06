//go:build windows

package catalog

import (
	"syscall"
	"unsafe"
)

// freeDiskBytes renvoie l'espace libre (octets) du volume contenant path ; ok=false si inconnu.
func freeDiskBytes(path string) (free int64, ok bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	var avail, total, totalFree uint64
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, false
	}
	return int64(avail), true
}
