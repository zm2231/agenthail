package systemone

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

func LoadConfig(home, key string) (Config, bool, error) {
	config, err := surface.ReadAgenthailConfig(home)
	if err != nil {
		return Config{}, false, err
	}
	raw, ok := config[key]
	if !ok {
		return Config{}, false, nil
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, false, invalid(home, key)
	}
	c.URL, c.Model = strings.TrimSpace(c.URL), strings.TrimSpace(c.Model)
	parsed, err := url.Parse(c.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || c.Model == "" {
		return Config{}, false, invalid(home, key)
	}
	return c, true, nil
}

func invalid(home, key string) error {
	return fmt.Errorf("the Agenthail config %s: %s must be {\"url\": \"http(s)://host:port\", \"model\": \"...\"}", surface.AgenthailConfigPath(home), key)
}
