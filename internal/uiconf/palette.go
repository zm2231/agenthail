package uiconf

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed palette.json
var paletteJSON []byte

type Pair struct {
	Dark  string `json:"dark"`
	Light string `json:"light"`
}

type Palette struct {
	Roles    map[string]Pair            `json:"roles"`
	Surfaces map[string]map[string]Pair `json:"surfaces"`
}

var BuiltIn = func() Palette {
	var p Palette
	if err := json.Unmarshal(paletteJSON, &p); err != nil {
		panic("uiconf: palette.json: " + err.Error())
	}
	return p
}()

func PaletteJSON() []byte { return paletteJSON }

func surfacesFor(class string) map[string]Pair {
	if class == "tablet" {
		class = "phone"
	}
	if surfaces, ok := BuiltIn.Surfaces[class]; ok {
		return surfaces
	}
	return BuiltIn.Surfaces["web"]
}

func surfaceList(surfaces map[string]Pair, dark bool) []string {
	list := make([]string, 0, len(surfaces))
	for _, pair := range surfaces {
		if dark {
			list = append(list, pair.Dark)
		} else {
			list = append(list, pair.Light)
		}
	}
	return list
}

func PaletteCSS() string {
	var b strings.Builder
	write := func(selector string, dark bool) {
		fmt.Fprintf(&b, "%s {\n", selector)
		for _, role := range TextRoles {
			pair, ok := BuiltIn.Roles[role]
			if !ok {
				continue
			}
			value := pair.Light
			if dark {
				value = pair.Dark
			}
			fmt.Fprintf(&b, "  --color-%s: %s;\n", strings.ReplaceAll(role, ".", "-"), value)
		}
		b.WriteString("}\n")
	}
	write(":root", true)
	write(`:root[data-theme="light"]`, false)
	return b.String()
}
