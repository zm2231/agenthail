// Package cfaccess verifies Cloudflare Access application JWTs.
package cfaccess

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	Header = "Cf-Access-Jwt-Assertion"
	Skew   = 60 * time.Second
	// Forged key ids must not make the daemon hammer the certs endpoint.
	refetchGap = time.Minute
)

var ErrInvalid = errors.New("invalid Access token")

// A service token has an empty Subject and its Client ID in CommonName.
type Claims struct {
	CommonName string `json:"common_name"`
	Email      string `json:"email"`
	jwt.RegisteredClaims
}

func Team(team string) string {
	team = strings.TrimSpace(strings.ToLower(team))
	team = strings.TrimPrefix(team, "https://")
	team = strings.TrimSuffix(team, "/")
	if team != "" && !strings.Contains(team, ".") {
		team += ".cloudflareaccess.com"
	}
	return team
}

// Access writes common_name lowercased with ".access"; people paste it without.
func ClientID(id string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(id)), ".access")
}

type Verifier struct {
	issuer   string
	aud      string
	certsURL string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	fetchErr  error
	tried     time.Time
}

func New(team, aud, certsURL string, client *http.Client) *Verifier {
	domain := Team(team)
	if certsURL == "" {
		certsURL = "https://" + domain + "/cdn-cgi/access/certs"
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Verifier{issuer: "https://" + domain, aud: strings.TrimSpace(aud), certsURL: certsURL, client: client, now: time.Now, keys: map[string]*rsa.PublicKey{}}
}

func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	var claims Claims
	parsed, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("the token names no key")
		}
		return v.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.issuer), jwt.WithAudience(v.aud), jwt.WithLeeway(Skew), jwt.WithExpirationRequired(), jwt.WithTimeFunc(v.now))
	if err != nil || !parsed.Valid {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return claims, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	if !v.tried.IsZero() && v.now().Sub(v.tried) < refetchGap {
		return nil, fmt.Errorf("unknown key %q", kid)
	}
	v.tried = v.now()
	keys, err := v.fetch(ctx)
	v.fetchErr = err
	if err != nil {
		return nil, fmt.Errorf("read the Access keys: %w", err)
	}
	v.keys, v.fetchedAt = keys, v.now()
	if key, ok := keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("unknown key %q", kid)
}

func (v *Verifier) Status() (time.Time, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fetchedAt, v.fetchErr
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := v.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", v.certsURL, response.Status)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, key := range set.Keys {
		if key.Kty != "RSA" || key.Kid == "" {
			continue
		}
		n, nErr := base64.RawURLEncoding.DecodeString(key.N)
		e, eErr := base64.RawURLEncoding.DecodeString(key.E)
		if nErr != nil || eErr != nil || len(e) > 4 {
			continue
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent<<8 | int(b)
		}
		keys[key.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
	}
	if len(keys) == 0 {
		return nil, errors.New("the key set holds no RSA keys")
	}
	return keys, nil
}
