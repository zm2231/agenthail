// Package cfaccesstest serves a fake Access key set and signs tokens with it.
package cfaccesstest

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	Team = "example-team"
	AUD  = "test-aud-tag"
)

type Issuer struct {
	Server  *httptest.Server
	Key     *rsa.PrivateKey
	KeyID   string
	Fetches atomic.Int32
}

func New(t testing.TB) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &Issuer{Key: key, KeyID: "test-key"}
	issuer.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		issuer.Fetches.Add(1)
		e := big.NewInt(int64(key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": issuer.KeyID, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	}))
	t.Cleanup(issuer.Server.Close)
	return issuer
}

// CertsURL is the fake key set's address.
func (i *Issuer) CertsURL() string { return i.Server.URL + "/cdn-cgi/access/certs" }

// Token is the claims of a valid service-token JWT for clientID, for changing before Sign.
func Token(clientID string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss": "https://" + Team + ".cloudflareaccess.com", "aud": []string{AUD},
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(),
		"sub": "", "common_name": clientID, "type": "app",
	}
}

func (i *Issuer) Sign(t testing.TB, claims jwt.MapClaims) string {
	return i.SignWith(t, claims, i.Key, i.KeyID)
}

func (i *Issuer) SignWith(t testing.TB, claims jwt.MapClaims, key *rsa.PrivateKey, kid string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
