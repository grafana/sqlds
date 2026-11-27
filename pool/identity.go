package pool

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// Identity returns the user part of a pool key for a request, or "" with no forwarded credential. A
// credential with no user claim gives an identity made from the credential, so users never share a pool.
func Identity(user *backend.User, headers http.Header) string {
	token := headers.Get(backend.OAuthIdentityTokenHeaderName)
	idToken := headers.Get(backend.OAuthIdentityIDTokenHeaderName)
	if token == "" && idToken == "" {
		return ""
	}
	if c, ok := grafanaIDClaims(headers.Get(backend.GrafanaUserSignInTokenHeaderName)); ok {
		return hashed("id", c.AuthenticatedBy+"\n"+c.Subject)
	}
	if user != nil && user.Login != "" {
		return hashed("login", user.Login)
	}
	return hashed("token", token+"\n"+idToken)
}

func hashed(kind, value string) string {
	sum := sha256.Sum256([]byte(value))
	return kind + ":" + hex.EncodeToString(sum[:])
}

type idClaims struct {
	Subject         string `json:"sub"`
	AuthenticatedBy string `json:"authenticatedBy"`
}

// grafanaIDClaims skips the signature check because Grafana sets the
// X-Grafana-Id header itself, so a client cannot supply it.
func grafanaIDClaims(token string) (idClaims, bool) {
	var c idClaims
	if !jwtPayload(token, &c) || c.Subject == "" {
		return idClaims{}, false
	}
	return c, true
}

// jwtPayload decodes the payload of a JWT into v. It does not check the signature.
func jwtPayload(token string, v any) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	return err == nil && json.Unmarshal(payload, v) == nil
}
