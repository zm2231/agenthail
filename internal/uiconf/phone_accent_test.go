package uiconf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestThePhoneAccentNeverSetsTheGlassLevel(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "native", "iOS", "SessionStyle.swift"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`accentHex: PaletteTokens\.Pair = \(0x(\w{6}), 0x(\w{6})\)`).FindStringSubmatch(string(source))
	if m == nil {
		t.Fatal("SessionStyle.accentHex not found")
	}
	accent := Pair{Light: "#" + m[1], Dark: "#" + m[2]}
	dimmest, brightest := 1.0, 0.0
	for _, role := range BuiltIn.Roles {
		dimmest = min(dimmest, luminance(hexRGB(role.Dark)))
		brightest = max(brightest, luminance(hexRGB(role.Light)))
	}
	if l := luminance(hexRGB(accent.Dark)); l < dimmest {
		t.Errorf("dark accent %s (luminance %.4f) is dimmer than every built-in role (dimmest %.4f), so floating glass solved for it starts above its calibrated level", accent.Dark, l, dimmest)
	}
	if l := luminance(hexRGB(accent.Light)); l > brightest {
		t.Errorf("light accent %s (luminance %.4f) is brighter than every built-in role (brightest %.4f), so floating glass solved for it starts above its calibrated level", accent.Light, l, brightest)
	}
	for name, surface := range BuiltIn.Surfaces["phone"] {
		for _, c := range []struct {
			dark   bool
			accent string
			ground string
		}{{true, accent.Dark, surface.Dark}, {false, accent.Light, surface.Light}} {
			if got := contrast(hexRGB(c.accent), hexRGB(c.ground)); got < 4.5 {
				t.Errorf("accent %s on phone %s %s: %s < 4.5", c.accent, name, c.ground, fmt.Sprintf("%.2f", got))
			}
		}
	}
}
