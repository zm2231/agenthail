package uiconf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNativePaletteTokensMatchPaletteJSON(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "native", "PaletteTokens.swift"))
	if err != nil {
		t.Fatal(err)
	}
	pair := func(light, dark string) Pair {
		return Pair{Dark: "#" + strings.ToLower(dark), Light: "#" + strings.ToLower(light)}
	}

	surfaces := map[string]Pair{}
	for _, m := range regexp.MustCompile(`static let (\w+): Pair = \(0x(\w{6}), 0x(\w{6})\)`).FindAllStringSubmatch(string(source), -1) {
		surfaces[m[1]] = pair(m[2], m[3])
	}
	if fmt.Sprint(surfaces) != fmt.Sprint(BuiltIn.Surfaces["mac"]) {
		t.Errorf("PaletteTokens surfaces %v, palette.json mac %v", surfaces, BuiltIn.Surfaces["mac"])
	}
	if surfaces["window"] != BuiltIn.Surfaces["phone"]["window"] {
		t.Errorf("the phone ground %v is PaletteTokens.window %v", BuiltIn.Surfaces["phone"]["window"], surfaces["window"])
	}

	names := map[string]string{"needsYou": "needs-you", "diffRemoved": "diff.removed"}
	roles := map[string]Pair{}
	for _, m := range regexp.MustCompile(`\.(\w+): \(0x(\w{6}), 0x(\w{6})\)`).FindAllStringSubmatch(string(source), -1) {
		name := m[1]
		if json, ok := names[name]; ok {
			name = json
		}
		roles[name] = pair(m[2], m[3])
	}
	if fmt.Sprint(roles) != fmt.Sprint(BuiltIn.Roles) {
		t.Errorf("PaletteTokens roles %v, palette.json %v", roles, BuiltIn.Roles)
	}
}
