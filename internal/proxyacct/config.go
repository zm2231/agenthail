package proxyacct

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

const KindCLIProxyAPI = "cliproxyapi"

type Config struct {
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	URL        string  `json:"url"`
	Management *KeyRef `json:"management,omitempty"`
}

var proxyName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ParseConfigs(raw json.RawMessage) ([]Config, error) {
	var configs []Config
	if err := json.Unmarshal(raw, &configs); err != nil || configs == nil {
		return nil, fmt.Errorf("proxies must be a list of {name, kind, url, management}")
	}
	seen := map[string]bool{}
	for i := range configs {
		if err := configs[i].normalize(); err != nil {
			return nil, err
		}
		if seen[configs[i].Name] {
			return nil, fmt.Errorf("proxy %q is listed twice", configs[i].Name)
		}
		seen[configs[i].Name] = true
	}
	return configs, nil
}

func (c *Config) normalize() error {
	if !proxyName.MatchString(c.Name) {
		return fmt.Errorf("proxy name %q must be letters, digits, '.', '_' or '-'", c.Name)
	}
	if c.Kind == "" {
		c.Kind = KindCLIProxyAPI
	}
	if c.Kind != KindCLIProxyAPI {
		return fmt.Errorf("proxy %s: kind %q is not supported; use %q", c.Name, c.Kind, KindCLIProxyAPI)
	}
	if _, err := Endpoint(c.URL); err != nil {
		return fmt.Errorf("proxy %s: %w", c.Name, err)
	}
	c.URL = strings.TrimRight(c.URL, "/")
	if m := c.Management; m != nil {
		if m.set() != 1 {
			return fmt.Errorf("proxy %s: management must name one of file, keychain, env or command", c.Name)
		}
		if m.File != "" && !filepath.IsAbs(m.File) {
			return fmt.Errorf("proxy %s: management file must be an absolute path", c.Name)
		}
		if m.Keychain != nil && (m.Keychain.Service == "" || m.Keychain.Account == "") {
			return fmt.Errorf("proxy %s: management keychain needs service and account", c.Name)
		}
		if len(m.Command) > 0 && !filepath.IsAbs(m.Command[0]) {
			return fmt.Errorf("proxy %s: management command must start with an absolute path", c.Name)
		}
	}
	return nil
}

// Endpoint is a URL's scheme, host and port, with every loopback address the
// same host, so a base URL and a configured proxy URL can be compared.
func Endpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", fmt.Errorf("url %q must be an http or https URL", raw)
	}
	host := strings.ToLower(parsed.Hostname())
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		host = "loopback"
	}
	port := parsed.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[parsed.Scheme]
	}
	return parsed.Scheme + "://" + net.JoinHostPort(host, port), nil
}

func IsLoopback(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Hostname() == "loopback"
}

func Match(configs []Config, baseURL string) (Config, bool) {
	endpoint, err := Endpoint(baseURL)
	if err != nil {
		return Config{}, false
	}
	for _, config := range configs {
		if other, err := Endpoint(config.URL); err == nil && other == endpoint {
			return config, true
		}
	}
	return Config{}, false
}
