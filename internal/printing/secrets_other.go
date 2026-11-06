//go:build !windows

package printing

import "errors"

// Hors Windows : pas de trousseau OS sans dépendance cgo ; la clé est un
// fichier 0600 (avertissement émis à l'ouverture du coffre).
func wrapKey(key []byte) ([]byte, string, error) { return key, "file-0600", nil }

func unwrapKey(raw []byte) ([]byte, string, error) {
	if len(raw) != 32 {
		return nil, "", errors.New("clé maître de taille inattendue")
	}
	return raw, "file-0600", nil
}
