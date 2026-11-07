package catalog

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// URL signées à portée (« scope ») : même clé HMAC et même vérification que les aperçus GLB, mais domaine de
// signature distinct (« scope:id:exp »). Une signature d'aperçu ne vaut donc jamais pour une archive d'export, ni
// l'inverse. Sert à l'export de projet (internal/export).

func (s *Service) signScoped(scope, id string, exp int64) string {
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "%s:%s:%d", scope, id, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// SignedScopedURL renvoie le chemin signé « /api/<segment>/<id><ext>?exp=…&sig=… », son URL absolue (base du
// service) et son expiration. Un ttl négatif produit une URL déjà expirée (tests).
func (s *Service) SignedScopedURL(scope, segment, id, ext string, ttl time.Duration) (path, abs string, exp time.Time) {
	exp = s.d.Store.Now().Add(ttl)
	path = fmt.Sprintf("/api/%s/%s%s?exp=%d&sig=%s", strings.Trim(segment, "/"), id, ext, exp.Unix(), s.signScoped(scope, id, exp.Unix()))
	return path, s.BaseURL() + path, exp
}

// VerifyScoped contrôle expiration et signature d'une URL signée à portée (temps constant).
func (s *Service) VerifyScoped(scope, id, expStr, sig string) bool {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || s.d.Store.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.signScoped(scope, id, exp)))
}
