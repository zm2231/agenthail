package uiconf

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Variants is a background drawn one way in dark appearance and another in
// light.
type Variants struct {
	Dark  string
	Light string
}

// Presets are the built-in gradients, written as the meshes a person could
// copy into ui.conf and edit.
var Presets = map[string]Variants{
	"dusk": {
		Dark:  "mesh: 3x3, #171934, #181a36, #171934, #94425e, #944247, #94425c, #1f1324, #0f0a14, #1f1324",
		Light: "mesh: 3x3, #b7bae6, #b8bae6, #b7bae6, #f2d9e2, #f2d9db, #f2d9e1, #d4b4e2, #c7aee0, #d4b4e2",
	},
	"slate": {
		Dark:  "mesh: 3x3, #1b2230, #222c3d, #1a2433, #2a3447, #33405a, #263042, #161c27, #1d2533, #141a24",
		Light: "mesh: 3x3, #b9c7e2, #c3d0e7, #b9cae4, #cdd7ea, #dbe2f0, #c8d4e8, #b2c2df, #bccae3, #afc1df",
	},
	"forest": {
		Dark:  "mesh: 3x3, #0f2a22, #163a2d, #0e2620, #1d4a38, #2a5d47, #1a3f31, #0c1f19, #12302a, #0b1b16",
		Light: "mesh: 3x3, #b2e8d8, #beeada, #b0e7d9, #c9eedf, #d9f2e7, #c3ebdc, #ace5d3, #b7e8df, #abe3d1",
	},
	"ember": {
		Dark:  "mesh: 3x3, #2a1410, #3d1d14, #2a120d, #682c1a, #863c21, #582516, #1f0f0b, #2e1610, #1a0c09",
		Light: "mesh: 3x3, #e7bab2, #ebc3b8, #e9b8ae, #f2d2c8, #f5ded6, #f0cbc1, #e6b7ab, #e8bdb2, #e5b3a9",
	},
	"violet": {
		Dark:  "mesh: 3x3, #1e1238, #2b1a52, #1a1033, #3a2270, #4a2c8a, #31205f, #150c28, #20143d, #120a22",
		Light: "mesh: 3x3, #c3b2e9, #ccbeed, #c0afe9, #d6caf1, #e0d7f4, #d0c5ee, #beaae8, #c4b5ea, #bca7e7",
	},
	"mono": {
		Dark:  "mesh: 3x3, #141414, #1c1c1c, #121212, #222222, #2a2a2a, #1e1e1e, #101010, #181818, #0e0e0e",
		Light: "mesh: 3x3, #cdcdcd, #d6d6d6, #cbcbcb, #dddddd, #e6e6e6, #d8d8d8, #c9c9c9, #d2d2d2, #c7c7c7",
	},
}

// PresetNames lists the presets in the order show-defaults prints them.
var PresetNames = []string{"dusk", "slate", "forest", "ember", "violet", "mono"}

// ArtNames lists the built-in pictures, each drawn for dark and for light.
var ArtNames = []string{"tide", "aurora", "dawn", "field"}

// ArtVariants is the picture pair for a built-in name, as background values.
func ArtVariants(name string) (Variants, bool) {
	if !contains(ArtNames, name) {
		return Variants{}, false
	}
	return Variants{Dark: "image: " + artDir + "/" + name + "-dark.jpg", Light: "image: " + artDir + "/" + name + "-light.jpg"}, true
}

var imageExtensions = []string{".png", ".jpg", ".jpeg", ".heic", ".webp"}

func checkBackground(value string) (string, string) {
	kind, spec, _ := strings.Cut(value, ":")
	kind, spec = strings.TrimSpace(kind), strings.TrimSpace(spec)
	switch {
	case value == "none" || value == "desktop":
		return value, ""
	case kind == "image":
		if spec == "" {
			return "", "background image needs a path: image: <path>"
		}
		if !contains(imageExtensions, strings.ToLower(filepath.Ext(spec))) {
			return "", "background image must be " + strings.Join(imageExtensions, ", ")
		}
		return "image: " + spec, ""
	case kind == "art":
		if _, ok := ArtVariants(spec); !ok {
			return "", "art must be one of " + strings.Join(ArtNames, ", ")
		}
		return "art: " + spec, ""
	case kind == "gradient":
		if _, ok := Presets[spec]; ok {
			return "gradient: " + spec, ""
		}
		if problem := checkLinear(spec); problem != "" {
			return "", problem
		}
		return "gradient: " + spec, ""
	case kind == "mesh":
		if problem := checkMesh(spec); problem != "" {
			return "", problem
		}
		return "mesh: " + spec, ""
	}
	return "", "background must be none, desktop, art: <name>, image: <path>, gradient: <preset or stops>, or mesh: <cols>x<rows>, <colours>"
}

func checkLinear(spec string) string {
	parts := splitList(spec)
	usage := "gradient must be a preset (" + strings.Join(PresetNames, ", ") + ") or <angle>deg, <colour> <pos>%, <colour> <pos>%, ..."
	if len(parts) < 3 || !strings.HasSuffix(parts[0], "deg") {
		return usage
	}
	if _, err := strconv.ParseFloat(strings.TrimSuffix(parts[0], "deg"), 64); err != nil {
		return usage
	}
	for _, stop := range parts[1:] {
		fields := strings.Fields(stop)
		if len(fields) != 2 || !hexColor.MatchString(fields[0]) || !strings.HasSuffix(fields[1], "%") {
			return usage
		}
		pos, err := strconv.ParseFloat(strings.TrimSuffix(fields[1], "%"), 64)
		if err != nil || pos < 0 || pos > 100 {
			return usage
		}
	}
	return ""
}

func checkMesh(spec string) string {
	parts := splitList(spec)
	if len(parts) == 0 {
		return "mesh must be <cols>x<rows>, then one colour per point"
	}
	cols, rows, ok := strings.Cut(parts[0], "x")
	c, errC := strconv.Atoi(cols)
	r, errR := strconv.Atoi(rows)
	if !ok || errC != nil || errR != nil || c < 2 || r < 2 || c > 6 || r > 6 {
		return "mesh size must be from 2x2 to 6x6"
	}
	if len(parts)-1 != c*r {
		return fmt.Sprintf("a %dx%d mesh needs %d colours, found %d", c, r, c*r, len(parts)-1)
	}
	for _, colour := range parts[1:] {
		if !hexColor.MatchString(colour) {
			return "mesh colours must be #rrggbb"
		}
	}
	return ""
}

func splitList(spec string) []string {
	var parts []string
	for _, part := range strings.Split(spec, ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// expandBackground turns a preset or built-in picture into what to draw in
// each appearance, so clients only read explicit backgrounds.
func expandBackground(value string) Variants {
	if name, ok := strings.CutPrefix(value, "art: "); ok {
		if art, ok := ArtVariants(name); ok {
			return art
		}
	}
	if name, ok := strings.CutPrefix(value, "gradient: "); ok {
		if preset, ok := Presets[name]; ok {
			return preset
		}
	}
	return Variants{Dark: value, Light: value}
}
