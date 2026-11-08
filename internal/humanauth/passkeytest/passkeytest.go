// Package passkeytest fournit un authentificateur WebAuthn VIRTUEL pour les tests (comme net/http/httptest pour le
// HTTP) : il génère une clé de test (ES256 ou EdDSA), fabrique les réponses de navigator.credentials.create et get, et
// permet de provoquer chaque défaut à vérifier (mauvaise origine, compteur qui régresse, vérification de l'utilisateur
// absente, challenge d'une autre demande...). Il ne sert qu'aux tests : aucune clé réelle, aucun accès réseau.
package passkeytest

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// Algorithmes COSE.
const (
	ES256 = -7
	EdDSA = -8
)

// Authenticator est un authentificateur virtuel.
type Authenticator struct {
	Alg    int
	CredID []byte
	// Counter : dernier compteur envoyé. Get l'incrémente avant d'écrire la réponse ; un test peut le fixer pour
	// simuler une régression ou un rejeu.
	Counter uint32
	// NoUserVerification : la réponse n'a pas le drapeau « utilisateur vérifié ».
	NoUserVerification bool
	// NoUserPresence : la réponse n'a pas le drapeau « utilisateur présent ».
	NoUserPresence bool

	ec *ecdsa.PrivateKey
	ed ed25519.PrivateKey
}

// New crée un authentificateur ES256 (alg = ES256) ou EdDSA (alg = EdDSA).
func New(alg int) (*Authenticator, error) {
	a := &Authenticator{Alg: alg, CredID: make([]byte, 32)}
	if _, err := rand.Read(a.CredID); err != nil {
		return nil, err
	}
	switch alg {
	case ES256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		a.ec = k
	case EdDSA:
		_, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		a.ed = k
	default:
		return nil, fmt.Errorf("algorithme %d non géré", alg)
	}
	return a, nil
}

// Must est New qui panique en cas d'erreur (tests).
func Must(alg int) *Authenticator {
	a, err := New(alg)
	if err != nil {
		panic(err)
	}
	return a
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rpHash(rpid string) []byte { h := sha256.Sum256([]byte(rpid)); return h[:] }

func (a *Authenticator) flags(attested bool) byte {
	var f byte
	if !a.NoUserPresence {
		f |= 0x01
	}
	if !a.NoUserVerification {
		f |= 0x04
	}
	if attested {
		f |= 0x40
	}
	return f
}

func (a *Authenticator) coseKey() ([]byte, error) {
	switch a.Alg {
	case ES256:
		x := a.ec.PublicKey.X.FillBytes(make([]byte, 32))
		y := a.ec.PublicKey.Y.FillBytes(make([]byte, 32))
		return cbor.Marshal(map[int]any{1: 2, 3: ES256, -1: 1, -2: x, -3: y})
	default:
		return cbor.Marshal(map[int]any{1: 1, 3: EdDSA, -1: 6, -2: []byte(a.ed.Public().(ed25519.PublicKey))})
	}
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
	} `json:"publicKey"`
}

type requestOptions struct {
	PublicKey struct {
		Challenge        string `json:"challenge"`
		RPID             string `json:"rpId"`
		UserVerification string `json:"userVerification"`
	} `json:"publicKey"`
}

// Challenge extrait le challenge (base64url) d'options de création ou de demande.
func Challenge(optionsJSON []byte) (string, error) {
	var o requestOptions
	if err := json.Unmarshal(optionsJSON, &o); err != nil || o.PublicKey.Challenge == "" {
		return "", errors.New("options sans challenge")
	}
	return o.PublicKey.Challenge, nil
}

// Create joue navigator.credentials.create : renvoie le JSON que le script envoie au serveur. origin est celui que le
// « navigateur » écrit dans clientDataJSON (un test le fausse pour vérifier le refus).
func (a *Authenticator) Create(optionsJSON []byte, origin string) ([]byte, error) {
	var o creationOptions
	if err := json.Unmarshal(optionsJSON, &o); err != nil || o.PublicKey.Challenge == "" {
		return nil, errors.New("options de création illisibles")
	}
	cose, err := a.coseKey()
	if err != nil {
		return nil, err
	}
	auth := append([]byte{}, rpHash(o.PublicKey.RP.ID)...)
	auth = append(auth, a.flags(true))
	auth = binary.BigEndian.AppendUint32(auth, a.Counter)
	auth = append(auth, make([]byte, 16)...) // AAGUID nul
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(a.CredID)))
	auth = append(auth, a.CredID...)
	auth = append(auth, cose...)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	if err != nil {
		return nil, err
	}
	cd := clientData("webauthn.create", o.PublicKey.Challenge, origin)
	return json.Marshal(map[string]any{
		"id": b64(a.CredID), "rawId": b64(a.CredID), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64(cd), "attestationObject": b64(att), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{},
	})
}

// Get joue navigator.credentials.get : incrémente le compteur, signe authData || SHA-256(clientDataJSON).
func (a *Authenticator) Get(optionsJSON []byte, origin string) ([]byte, error) {
	var o requestOptions
	if err := json.Unmarshal(optionsJSON, &o); err != nil || o.PublicKey.Challenge == "" {
		return nil, errors.New("options de demande illisibles")
	}
	return a.GetChallenge(o.PublicKey.RPID, o.PublicKey.Challenge, origin)
}

// GetChallenge signe un challenge donné (un test peut y mettre le challenge d'une AUTRE demande).
func (a *Authenticator) GetChallenge(rpid, challenge, origin string) ([]byte, error) {
	a.Counter++
	auth := append([]byte{}, rpHash(rpid)...)
	auth = append(auth, a.flags(false))
	auth = binary.BigEndian.AppendUint32(auth, a.Counter)
	cd := clientData("webauthn.get", challenge, origin)
	ch := sha256.Sum256(cd)
	msg := append(append([]byte{}, auth...), ch[:]...)
	var sig []byte
	var err error
	switch a.Alg {
	case ES256:
		d := sha256.Sum256(msg)
		sig, err = ecdsa.SignASN1(rand.Reader, a.ec, d[:])
		if err != nil {
			return nil, err
		}
	default:
		sig = ed25519.Sign(a.ed, msg)
	}
	return json.Marshal(map[string]any{
		"id": b64(a.CredID), "rawId": b64(a.CredID), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64(cd), "authenticatorData": b64(auth), "signature": b64(sig), "userHandle": b64([]byte("bambud-human-owner-v1"))},
		"clientExtensionResults": map[string]any{},
	})
}
