// Package mcpoauth is the authorization server side of the MCP authorization spec.
package mcpoauth

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

const (
	ResourcePath          = "/mcp"
	ProtectedResourcePath = "/.well-known/oauth-protected-resource"
	AuthServerPath        = "/.well-known/oauth-authorization-server"
	AuthorizePath         = "/oauth/authorize"
	TokenPath             = "/oauth/token"
	RegisterPath          = "/oauth/register"
	RevokePath            = "/oauth/revoke"
)

var Paths = []string{ProtectedResourcePath, ProtectedResourcePath + ResourcePath, AuthServerPath, AuthorizePath, TokenPath, RegisterPath, RevokePath}

func ResourceMetadataURL(issuer string) string { return issuer + ProtectedResourcePath + ResourcePath }

func Resource(issuer string) string { return issuer + ResourcePath }

func ProtectedResource(issuer string) map[string]any {
	return map[string]any{
		"resource":                 Resource(issuer),
		"authorization_servers":    []string{issuer},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Agenthail",
	}
}

func AuthorizationServer(issuer string) map[string]any {
	return map[string]any{
		"issuer":                                         issuer,
		"authorization_endpoint":                         issuer + AuthorizePath,
		"token_endpoint":                                 issuer + TokenPath,
		"registration_endpoint":                          issuer + RegisterPath,
		"revocation_endpoint":                            issuer + RevokePath,
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"authorization_response_iss_parameter_supported": true,
	}
}

func ParseIssuer(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	switch {
	case u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "":
		return "", errors.New("give the origin only, such as https://agent.example.com")
	case u.Scheme != "https" && !(u.Scheme == "http" && loopbackHost(u.Hostname())):
		return "", errors.New("the URL must be https, or http on a loopback host")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
