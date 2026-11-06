//go:build linux || darwin

package catalog

import "syscall"

// freeDiskBytes renvoie l'espace libre (octets) du volume contenant path ; ok=false si inconnu.
func freeDiskBytes(path string) (free int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}
