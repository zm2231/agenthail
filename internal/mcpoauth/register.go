package mcpoauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	maxRedirectURIs   = 8
	maxRedirectURI    = 2048
	maxClientName     = 80
	unnamedClientName = "an unnamed client"
)

type Error struct {
	Status      int    `json:"-"`
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Description }

func failure(status int, code, description string) *Error {
	return &Error{Status: status, Code: code, Description: description}
}

func WriteError(w http.ResponseWriter, err *Error) {
	WriteJSON(w, err.Status, err)
}

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type Registration struct {
	Name         string
	RedirectURIs []string
}

func ParseRegistration(body io.Reader) (Registration, *Error) {
	var meta struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	if err := json.NewDecoder(body).Decode(&meta); err != nil {
		return Registration{}, failure(http.StatusBadRequest, "invalid_client_metadata", "the body is not client metadata JSON")
	}
	if method := meta.TokenEndpointAuthMethod; method != "" && method != "none" {
		return Registration{}, failure(http.StatusBadRequest, "invalid_client_metadata", "only public clients register here; token_endpoint_auth_method must be none")
	}
	for _, grant := range meta.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return Registration{}, failure(http.StatusBadRequest, "invalid_client_metadata", "grant_types may hold only authorization_code and refresh_token")
		}
	}
	if len(meta.ResponseTypes) > 0 && !slices.Equal(meta.ResponseTypes, []string{"code"}) {
		return Registration{}, failure(http.StatusBadRequest, "invalid_client_metadata", "response_types may hold only code")
	}
	if len(meta.RedirectURIs) == 0 || len(meta.RedirectURIs) > maxRedirectURIs {
		return Registration{}, failure(http.StatusBadRequest, "invalid_redirect_uri", "give between 1 and 8 redirect_uris")
	}
	for _, uri := range meta.RedirectURIs {
		if err := checkRedirectURI(uri); err != nil {
			return Registration{}, err
		}
	}
	return Registration{Name: clientName(meta.ClientName), RedirectURIs: meta.RedirectURIs}, nil
}

func RegistrationResponse(clientID string, issuedAt time.Time, reg Registration) map[string]any {
	return map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        issuedAt.Unix(),
		"client_name":                reg.Name,
		"redirect_uris":              reg.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}
}

func clientName(raw string) string {
	name := strings.Join(strings.FieldsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }), " ")
	if runes := []rune(name); len(runes) > maxClientName {
		name = string(runes[:maxClientName])
	}
	if name == "" {
		return unnamedClientName
	}
	return name
}

func checkRedirectURI(raw string) *Error {
	u, err := url.Parse(raw)
	switch {
	case err != nil || len(raw) > maxRedirectURI:
		return failure(http.StatusBadRequest, "invalid_redirect_uri", "a redirect_uri is not a URL of at most 2048 bytes")
	case u.Fragment != "" || u.User != nil || u.Host == "":
		return failure(http.StatusBadRequest, "invalid_redirect_uri", "a redirect_uri has a fragment, credentials or no host")
	case u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname())):
		return failure(http.StatusBadRequest, "invalid_redirect_uri", "a redirect_uri must be https, or http on a loopback host")
	}
	return nil
}

// A loopback redirect may name any port, since a native client picks a free one each time (RFC 8252).
func RedirectMatches(registered []string, given string) bool {
	for _, uri := range registered {
		if uri == given {
			return true
		}
		want, err := url.Parse(uri)
		if err != nil || want.Scheme != "http" || !loopbackHost(want.Hostname()) {
			continue
		}
		got, err := url.Parse(given)
		if err == nil && got.Scheme == "http" && got.Hostname() == want.Hostname() && got.EscapedPath() == want.EscapedPath() && got.RawQuery == want.RawQuery && got.Fragment == "" && got.User == nil {
			return true
		}
	}
	return false
}
