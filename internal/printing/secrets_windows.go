//go:build windows

package printing

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wrapKey protège la clé maître avec DPAPI (liée au compte Windows courant).
func wrapKey(key []byte) ([]byte, string, error) {
	in := windows.DataBlob{Size: uint32(len(key)), Data: &key[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	b := make([]byte, out.Size)
	copy(b, unsafe.Slice(out.Data, out.Size))
	return b, "dpapi", nil
}

func unwrapKey(raw []byte) ([]byte, string, error) {
	if len(raw) == 0 {
		return nil, "", errors.New("clé vide")
	}
	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	b := make([]byte, out.Size)
	copy(b, unsafe.Slice(out.Data, out.Size))
	if len(b) != 32 {
		return nil, "", errors.New("clé maître de taille inattendue")
	}
	return b, "dpapi", nil
}
