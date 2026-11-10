package goalsort

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const ConfigKey = "ledgerSorter"

func LoadConfig(home string) (Config, bool, error) {
	config, err := surface.ReadAgenthailConfig(home)
	if err != nil {
		return Config{}, false, err
	}
	raw, ok := config[ConfigKey]
	if !ok {
		return Config{}, false, nil
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, false, invalid(home)
	}
	c.URL, c.Model = strings.TrimSpace(c.URL), strings.TrimSpace(c.Model)
	parsed, err := url.Parse(c.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || c.Model == "" {
		return Config{}, false, invalid(home)
	}
	return c, true, nil
}

func invalid(home string) error {
	return fmt.Errorf("Agenthail config %s: %s must be {\"url\": \"http(s)://host:port\", \"model\": \"...\"}", surface.AgenthailConfigPath(home), ConfigKey)
}
