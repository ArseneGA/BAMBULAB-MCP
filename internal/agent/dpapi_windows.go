//go:build windows

package agent

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapi protège un secret avec CryptProtectData (crypt32). Portée « utilisateur »
// par défaut : seul le même compte Windows sur le même poste peut le relire.
type dpapi struct{}

// NewProtector renvoie le protecteur DPAPI.
func NewProtector() Protector { return dpapi{} }

// entropy est un sel applicatif : un autre programme du même compte doit le
// connaître pour déchiffrer (défense en profondeur, pas un secret).
var entropy = []byte("BambuAtelier/agent/v1")

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func (dpapi) Protect(plain []byte, machine bool) ([]byte, error) {
	var out windows.DataBlob
	flags := uint32(windows.CRYPTPROTECT_UI_FORBIDDEN)
	if machine {
		flags |= windows.CRYPTPROTECT_LOCAL_MACHINE
	}
	desc, _ := windows.UTF16PtrFromString("BambuAtelier agent")
	if err := windows.CryptProtectData(blob(plain), desc, blob(entropy), 0, nil, flags, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func (dpapi) Unprotect(enc []byte, machine bool) ([]byte, error) {
	if len(enc) == 0 {
		return nil, errors.New("blob vide")
	}
	var out windows.DataBlob
	flags := uint32(windows.CRYPTPROTECT_UI_FORBIDDEN)
	if err := windows.CryptUnprotectData(blob(enc), nil, blob(entropy), 0, nil, flags, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
