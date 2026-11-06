package mesh

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Parse détecte le format (contenu d'abord, extension en indice) et lit le fichier.
// name sert d'indice d'extension et peut être vide.
func Parse(name string, data []byte) (*Model, error) {
	switch sniff(name, data) {
	case "3mf":
		return ParseThreeMF(data)
	case "obj":
		return ParseOBJ(data)
	case "stl":
		return ParseSTL(data)
	}
	return nil, fmt.Errorf("%w : %q", ErrFormat, name)
}

func sniff(name string, data []byte) string {
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("PK\x05\x06")) {
		return "3mf"
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".obj" {
		return "obj"
	}
	if bin, _ := isBinarySTL(data); bin || looksASCII(data) {
		return "stl"
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	for _, l := range strings.Split(string(head), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "v ") || strings.HasPrefix(l, "f ") {
			return "obj"
		}
	}
	if ext == ".stl" {
		return "stl"
	}
	return ""
}

// ReadFile lit un fichier du disque.
func ReadFile(p string) (*Model, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<31))
	if err != nil {
		return nil, err
	}
	return Parse(p, data)
}

// SHA256Hex retourne l'empreinte hexadécimale du contenu.
func SHA256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// SHA256Reader hache un flux et retourne l'empreinte et le nombre d'octets.
func SHA256Reader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
