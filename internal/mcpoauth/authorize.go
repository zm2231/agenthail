package mcpoauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
)

var AuthorizeParams = []string{"response_type", "client_id", "redirect_uri", "state", "code_challenge", "code_challenge_method", "resource", "scope"}

type AuthorizeRequest struct {
	ClientID    string
	RedirectURI string
	State       string
	Challenge   string
}

// An error about the client or redirect is shown to the person, never sent to an unverified redirect.
func ParseAuthorize(q url.Values, issuer string, redirects func(clientID string) ([]string, bool)) (AuthorizeRequest, *Error, bool) {
	req := AuthorizeRequest{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), State: q.Get("state"), Challenge: q.Get("code_challenge")}
	registered, ok := redirects(req.ClientID)
	if !ok {
		return req, failure(http.StatusBadRequest, "invalid_client", "this client is not registered here; register it again"), false
	}
	if req.RedirectURI == "" && len(registered) == 1 {
		req.RedirectURI = registered[0]
	}
	if !RedirectMatches(registered, req.RedirectURI) {
		return req, failure(http.StatusBadRequest, "invalid_request", "redirect_uri is not one this client registered"), false
	}
	switch {
	case q.Get("response_type") != "code":
		return req, failure(http.StatusBadRequest, "unsupported_response_type", "only response_type=code is served"), true
	case q.Get("code_challenge_method") != "S256" || !validChallenge(req.Challenge):
		return req, failure(http.StatusBadRequest, "invalid_request", "a code_challenge with code_challenge_method=S256 is required"), true
	case !ResourceMatches(q.Get("resource"), issuer):
		return req, failure(http.StatusBadRequest, "invalid_target", "resource must be "+Resource(issuer)), true
	}
	return req, nil, true
}

// A client that names no resource means the only one there is.
func ResourceMatches(resource, issuer string) bool {
	return resource == "" || strings.TrimSuffix(resource, "/") == Resource(issuer)
}

func Redirect(redirectURI string, params url.Values) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for key, values := range params {
		q[key] = values
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func ErrorRedirect(req AuthorizeRequest, issuer string, err *Error) string {
	params := url.Values{"error": {err.Code}, "error_description": {err.Description}, "iss": {issuer}}
	if req.State != "" {
		params.Set("state", req.State)
	}
	return Redirect(req.RedirectURI, params)
}

func CodeRedirect(req AuthorizeRequest, issuer, code string) string {
	params := url.Values{"code": {code}, "iss": {issuer}}
	if req.State != "" {
		params.Set("state", req.State)
	}
	return Redirect(req.RedirectURI, params)
}

func validChallenge(challenge string) bool {
	if len(challenge) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(challenge)
	return err == nil
}

func VerifyS256(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || strings.IndexFunc(verifier, func(r rune) bool { return !unreserved(r) }) >= 0 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func unreserved(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)
}
