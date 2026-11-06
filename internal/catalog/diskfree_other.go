//go:build !windows && !linux && !darwin

package catalog

func freeDiskBytes(path string) (int64, bool) { return 0, false }
