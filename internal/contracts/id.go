package contracts

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID renvoie un identifiant opaque préfixé, ex. "job_3f9a…" (96 bits d'aléa).
func NewID(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand indisponible : " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(b)
}
