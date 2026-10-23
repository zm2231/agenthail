package uiconf

import "strings"

var TextRoles = []string{"body", "muted", "faint", "meta", "link", "code", "working", "needs-you", "error", "done", "diff.removed", "peer"}

const IncreasedContrast = 7.0

type RoleColor struct {
	Dark         string   `json:"dark,omitempty"`
	Light        string   `json:"light,omitempty"`
	Increased    *Pair    `json:"increased,omitempty"`
	Lifted       []string `json:"lifted,omitempty"`
	SystemAccent bool     `json:"systemAccent,omitempty"`
}

func Colors(chosen map[string]string, class string, target float64) map[string]RoleColor {
	surfaces := surfacesFor(class)
	colors := make(map[string]RoleColor, len(TextRoles)+1)
	if accent, ok := parsePair(chosen["theme.accent"]); ok {
		colors["accent"] = liftPair(accent, surfaces, target)
	} else {
		colors["accent"] = RoleColor{SystemAccent: true}
	}
	for _, role := range TextRoles {
		value := chosen["color."+role]
		switch {
		case value == "accent":
			colors[role] = colors["accent"]
		case value == "default" || value == "":
			colors[role] = liftPair(BuiltIn.Roles[role], surfaces, target)
		default:
			pair, _ := parsePair(value)
			colors[role] = liftPair(pair, surfaces, target)
		}
	}
	return colors
}

func parsePair(value string) (Pair, bool) {
	dark, light, paired := strings.Cut(value, "/")
	if !paired {
		light = dark
	}
	if !hexColor.MatchString(dark) || !hexColor.MatchString(light) {
		return Pair{}, false
	}
	return Pair{Dark: strings.ToLower(dark), Light: strings.ToLower(light)}, true
}

func liftPair(pair Pair, surfaces map[string]Pair, target float64) RoleColor {
	var lifted []string
	one := func(hex string, dark bool, target float64, name string) string {
		out := Readable(hex, surfaceList(surfaces, dark), dark, target)
		if out != hex {
			lifted = append(lifted, name)
		}
		return out
	}
	color := RoleColor{
		Dark:      one(pair.Dark, true, target, "dark"),
		Light:     one(pair.Light, false, target, "light"),
		Increased: &Pair{Dark: one(pair.Dark, true, IncreasedContrast, "increased.dark"), Light: one(pair.Light, false, IncreasedContrast, "increased.light")},
	}
	color.Lifted = lifted
	return color
}
