package mcpoauth

import (
	"net/url"
	"strings"
	"testing"
)

func TestPKCEFollowsRFC7636(t *testing.T) {
	verifier, challenge := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !VerifyS256(verifier, challenge) {
		t.Fatal("the RFC 7636 appendix B pair was refused")
	}
	for _, bad := range []string{verifier[:42], verifier + "!", strings.Repeat("a", 129)} {
		if VerifyS256(bad, challenge) {
			t.Fatalf("verifier %q passed", bad)
		}
	}
}

func TestRedirectsMatchExactlyExceptALoopbackPort(t *testing.T) {
	registered := []string{"https://kat.example/cb", "http://127.0.0.1:1234/callback"}
	cases := map[string]bool{
		"https://kat.example/cb":          true,
		"https://kat.example/cb?x=1":      false,
		"https://kat.example/cb/":         false,
		"http://127.0.0.1:9999/callback":  true,
		"http://127.0.0.1:9999/other":     false,
		"http://localhost:1234/callback":  false,
		"https://127.0.0.1:1234/callback": false,
	}
	for given, want := range cases {
		if got := RedirectMatches(registered, given); got != want {
			t.Errorf("%s: matched=%v want %v", given, got, want)
		}
	}
}

func TestRegistrationTakesOnlyPublicClientsWithSafeRedirects(t *testing.T) {
	cases := map[string]string{
		`{"redirect_uris":["https://kat.example/cb"],"client_name":"Kat"}`:                                "",
		`{"redirect_uris":["http://localhost:3000/cb"]}`:                                                  "",
		`{"redirect_uris":["http://kat.example/cb"]}`:                                                     "invalid_redirect_uri",
		`{"redirect_uris":["https://kat.example/cb#x"]}`:                                                  "invalid_redirect_uri",
		`{"redirect_uris":["javascript:alert(1)"]}`:                                                       "invalid_redirect_uri",
		`{"redirect_uris":[]}`:                                                                            "invalid_redirect_uri",
		`{"redirect_uris":["https://kat.example/cb"],"token_endpoint_auth_method":"client_secret_basic"}`: "invalid_client_metadata",
		`{"redirect_uris":["https://kat.example/cb"],"grant_types":["client_credentials"]}`:               "invalid_client_metadata",
	}
	for body, want := range cases {
		_, err := ParseRegistration(strings.NewReader(body))
		got := ""
		if err != nil {
			got = err.Code
		}
		if got != want {
			t.Errorf("%s: error %q want %q", body, got, want)
		}
	}
}

func TestAnAuthorizeRequestWithAnUnknownRedirectIsNotSentBack(t *testing.T) {
	redirects := func(id string) ([]string, bool) { return []string{"https://kat.example/cb"}, id == "ahc_kat" }
	issuer := "https://agent.example.com"
	query := func(change func(url.Values)) url.Values {
		q := url.Values{"response_type": {"code"}, "client_id": {"ahc_kat"}, "redirect_uri": {"https://kat.example/cb"}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}, "resource": {issuer + "/mcp"}}
		change(q)
		return q
	}
	cases := []struct {
		name     string
		change   func(url.Values)
		code     string
		sendBack bool
	}{
		{"valid", func(url.Values) {}, "", true},
		{"unknown client", func(q url.Values) { q.Set("client_id", "ahc_other") }, "invalid_client", false},
		{"unregistered redirect", func(q url.Values) { q.Set("redirect_uri", "https://evil.example/cb") }, "invalid_request", false},
		{"plain challenge", func(q url.Values) { q.Set("code_challenge_method", "plain") }, "invalid_request", true},
		{"another resource", func(q url.Values) { q.Set("resource", "https://other.example/mcp") }, "invalid_target", true},
	}
	for _, tc := range cases {
		_, err, sendBack := ParseAuthorize(query(tc.change), issuer, redirects)
		got := ""
		if err != nil {
			got = err.Code
		}
		if got != tc.code || (err != nil && sendBack != tc.sendBack) {
			t.Errorf("%s: error %q sendBack=%v", tc.name, got, sendBack)
		}
	}
}

func TestTheIssuerIsAnOriginClientsCanTrust(t *testing.T) {
	cases := map[string]string{
		"https://Agent.Example.com/":     "https://agent.example.com",
		"http://localhost:7412":          "http://localhost:7412",
		"http://agent.example.com":       "",
		"https://agent.example.com/mcp":  "",
		"https://user@agent.example.com": "",
	}
	for raw, want := range cases {
		got, err := ParseIssuer(raw)
		if (err != nil) != (want == "") || got != want {
			t.Errorf("%s: got %q err %v", raw, got, err)
		}
	}
}
