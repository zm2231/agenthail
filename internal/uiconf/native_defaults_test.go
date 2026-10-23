package uiconf

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The apps fall back to these values for any key a config leaves out, so
// they must be the daemon's defaults. background is the one exception: until
// a config arrives the apps draw no background rather than guess one.
func TestNativeFallbacksAreTheDefaults(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "native", "UIConfig.swift"))
	if err != nil {
		t.Fatal(err)
	}
	getter := regexp.MustCompile(`(text|number|list)\("([a-z.\-]+)", (\[[^\]]*\]|"[^"]*"|[0-9.]+)\)`)
	seen := map[string]bool{}
	for _, match := range getter.FindAllStringSubmatch(string(source), -1) {
		kind, name, fallback := match[1], match[2], match[3]
		if name == "background" || name == "background.light" {
			continue
		}
		key, ok := Lookup(name)
		if !ok {
			t.Errorf("UIConfig.swift reads %s, which ui.conf does not have", name)
			continue
		}
		seen[name] = true
		var got string
		switch kind {
		case "text":
			got = strings.Trim(fallback, `"`)
		case "number":
			n, _ := strconv.ParseFloat(fallback, 64)
			want, _ := strconv.ParseFloat(key.Default, 64)
			if n != want {
				t.Errorf("UIConfig.swift falls back to %s = %s; the default is %s", name, fallback, key.Default)
			}
			continue
		case "list":
			items := regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(fallback, -1)
			var parts []string
			for _, item := range items {
				parts = append(parts, item[1])
			}
			got = strings.Join(parts, ", ")
		}
		if got != key.Default {
			t.Errorf("UIConfig.swift falls back to %s = %s; the default is %s", name, got, key.Default)
		}
	}
	if len(seen) < 20 {
		t.Fatalf("read %d getters from UIConfig.swift; the pattern no longer matches it", len(seen))
	}
}
