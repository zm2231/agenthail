package uiconf

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Presets are the built-in gradients, written as the mesh a person could
// copy into ui.conf and edit.
var Presets = map[string]string{
	"aurora": "mesh: 3x3, #2e1972, #182c58, #0e5671, #2d1655, #386870, #2e3340, #591948, #8a3049, #682e2c",
	"dusk":   "mesh: 3x3, #121439, #14163a, #121439, #c2557a, #d98b8f, #c4597b, #1f1324, #0f0a14, #1f1324",
	"slate":  "mesh: 3x3, #1b2230, #222c3d, #1a2433, #2a3447, #33405a, #263042, #161c27, #1d2533, #141a24",
	"forest": "mesh: 3x3, #0f2a22, #163a2d, #0e2620, #1d4a38, #2a5d47, #1a3f31, #0c1f19, #12302a, #0b1b16",
	"ember":  "mesh: 3x3, #2a1410, #3d1d14, #2a120d, #6b2a17, #8a3a1d, #5a2414, #1f0f0b, #2e1610, #1a0c09",
	"violet": "mesh: 3x3, #1e1238, #2b1a52, #1a1033, #3a2270, #4a2c8a, #31205f, #150c28, #20143d, #120a22",
	"mono":   "mesh: 3x3, #141414, #1c1c1c, #121212, #222222, #2a2a2a, #1e1e1e, #101010, #181818, #0e0e0e",
}

// PresetNames lists the presets in the order show-defaults prints them.
var PresetNames = []string{"aurora", "dusk", "slate", "forest", "ember", "violet", "mono"}

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
	return "", "background must be none, desktop, image: <path>, gradient: <preset or stops>, or mesh: <cols>x<rows>, <colours>"
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

// expandBackground turns a preset name into its mesh, so clients only read
// explicit gradients.
func expandBackground(value string) string {
	if name, ok := strings.CutPrefix(value, "gradient: "); ok {
		if mesh, ok := Presets[name]; ok {
			return mesh
		}
	}
	return value
}
