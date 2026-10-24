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
	table := func(section string) map[string]Pair {
		pairs := map[string]Pair{}
		for _, m := range regexp.MustCompile(`\.(\w+): \(0x(\w{6}), 0x(\w{6})\)`).FindAllStringSubmatch(section, -1) {
			name := m[1]
			if json, ok := names[name]; ok {
				name = json
			}
			pairs[name] = pair(m[2], m[3])
		}
		return pairs
	}
	rolesSource, increasedSource, found := strings.Cut(string(source), "static let increasedRoles")
	if !found {
		t.Fatal("PaletteTokens has no increasedRoles table")
	}
	if roles := table(rolesSource); fmt.Sprint(roles) != fmt.Sprint(BuiltIn.Roles) {
		t.Errorf("PaletteTokens roles %v, palette.json %v", roles, BuiltIn.Roles)
	}

	grounds := map[string]Pair{}
	for _, class := range []string{"mac", "phone"} {
		for name, surface := range BuiltIn.Surfaces[class] {
			grounds[class+"."+name] = surface
		}
	}
	want := map[string]Pair{}
	for role, colour := range BuiltIn.Roles {
		want[role] = Pair{
			Dark:  Readable(colour.Dark, surfaceList(grounds, true), true, IncreasedContrast),
			Light: Readable(colour.Light, surfaceList(grounds, false), false, IncreasedContrast),
		}
	}
	if increased := table(increasedSource); fmt.Sprint(increased) != fmt.Sprint(want) {
		t.Errorf("PaletteTokens increasedRoles %v, palette.json lifted to %.0f:1 on the Mac and phone surfaces %v", increased, IncreasedContrast, want)
	}
}
