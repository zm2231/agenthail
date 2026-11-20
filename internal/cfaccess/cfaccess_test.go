package cfaccess_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zm2231/agenthail/internal/cfaccess"
	"github.com/zm2231/agenthail/internal/cfaccess/cfaccesstest"
)

func TestAnAccessTokenIsCheckedForSignatureIssuerAudienceAndTime(t *testing.T) {
	issuer := cfaccesstest.New(t)
	verifier := cfaccess.New(cfaccesstest.Team, cfaccesstest.AUD, issuer.CertsURL(), nil)
	ctx := context.Background()

	claims, err := verifier.Verify(ctx, issuer.Sign(t, cfaccesstest.Token("abc123.access")))
	if err != nil || claims.CommonName != "abc123.access" || claims.Subject != "" {
		t.Fatalf("a valid token: claims=%+v err=%v", claims, err)
	}

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"wrong audience":  issuer.Sign(t, with(cfaccesstest.Token("abc123"), "aud", []string{"another-app"})),
		"wrong issuer":    issuer.Sign(t, with(cfaccesstest.Token("abc123"), "iss", "https://another.cloudflareaccess.com")),
		"expired":         issuer.Sign(t, with(cfaccesstest.Token("abc123"), "exp", time.Now().Add(-2*cfaccess.Skew).Unix())),
		"not yet valid":   issuer.Sign(t, with(cfaccesstest.Token("abc123"), "nbf", time.Now().Add(2*cfaccess.Skew).Unix())),
		"no expiry":       issuer.Sign(t, without(cfaccesstest.Token("abc123"), "exp")),
		"unknown key id":  issuer.SignWith(t, cfaccesstest.Token("abc123"), issuer.Key, "rotated-away"),
		"another key":     issuer.SignWith(t, cfaccesstest.Token("abc123"), other, issuer.KeyID),
		"not a token":     "not.a.token",
		"unsigned (none)": unsigned(t),
	}
	for name, token := range cases {
		if _, err := verifier.Verify(ctx, token); !errors.Is(err, cfaccess.ErrInvalid) {
			t.Errorf("%s: err=%v, want ErrInvalid", name, err)
		}
	}
	if fetches := issuer.Fetches.Load(); fetches != 1 {
		t.Fatalf("the key set was fetched %d times; an unknown key id may refetch at most once a minute", fetches)
	}
	if fetched, err := verifier.Status(); fetched.IsZero() || err != nil {
		t.Fatalf("status fetched=%v err=%v", fetched, err)
	}
}

func TestAccessNamesNormalize(t *testing.T) {
	for _, team := range []string{"example-team", "Example-Team.cloudflareaccess.com", "https://example-team.cloudflareaccess.com/"} {
		if got := cfaccess.Team(team); got != "example-team.cloudflareaccess.com" {
			t.Errorf("Team(%q)=%q", team, got)
		}
	}
	if cfaccess.ClientID(" ABC123.access ") != "abc123" || cfaccess.ClientID("abc123") != "abc123" {
		t.Fatal("a Client ID with and without .access must normalize to the same value")
	}
}

func with(claims jwt.MapClaims, key string, value any) jwt.MapClaims {
	claims[key] = value
	return claims
}

func without(claims jwt.MapClaims, key string) jwt.MapClaims {
	delete(claims, key)
	return claims
}

func unsigned(t *testing.T) string {
	token := jwt.NewWithClaims(jwt.SigningMethodNone, cfaccesstest.Token("abc123"))
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
