package uiconf

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type oklch struct{ l, c, h float64 }

func (c rgb) oklch() oklch {
	r, g, b := linear(c.r), linear(c.g), linear(c.b)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	lightness := 0.2104542553*l + 0.7936177850*m - 0.0040720468*s
	a := 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	bb := 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	return oklch{lightness, math.Hypot(a, bb), math.Mod(math.Atan2(bb, a)*180/math.Pi+360, 360)}
}

func (c oklch) lab() (float64, float64, float64) {
	return c.l, c.c * math.Cos(c.h*math.Pi/180), c.c * math.Sin(c.h*math.Pi/180)
}

func hueGap(a, b float64) float64 {
	gap := math.Abs(a - b)
	return min(gap, 360-gap)
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

func webTokens(t *testing.T) (dark, light map[string]string) {
	css := readRepoFile(t, "internal/daemon/dashboard/tokens.css")
	darkBlock, rest, _ := strings.Cut(css, `:root[data-theme="light"] {`)
	lightBlock, _, _ := strings.Cut(rest, "}")
	read := func(block string) map[string]string {
		values := map[string]string{}
		for _, m := range regexp.MustCompile(`--([\w-]+):\s*([^;]+);`).FindAllStringSubmatch(block, -1) {
			values[m[1]] = strings.TrimSpace(m[2])
		}
		return values
	}
	return read(darkBlock), read(lightBlock)
}

func cssHue(t *testing.T, value string) float64 {
	m := regexp.MustCompile(`^oklch\([\d.]+%\s+[\d.]+\s+([\d.]+)`).FindStringSubmatch(value)
	if m == nil {
		t.Fatalf("%q is not an oklch colour", value)
	}
	hue, _ := strconv.ParseFloat(m[1], 64)
	return hue
}

func TestProjectColoursReadOnTheirFill(t *testing.T) {
	if len(BuiltIn.Projects) != 6 {
		t.Fatalf("%d project colours, want 6", len(BuiltIn.Projects))
	}
	for _, colour := range BuiltIn.Projects {
		for _, shade := range []struct{ fill, ink string }{{colour.Fill.Light, colour.Ink.Light}, {colour.Fill.Dark, colour.Ink.Dark}} {
			if ratio := contrast(hexRGB(shade.fill), hexRGB(shade.ink)); ratio < IncreasedContrast {
				t.Errorf("%s: %s on %s reads at %.2f:1, want %.0f:1", colour.Name, shade.ink, shade.fill, ratio, IncreasedContrast)
			}
		}
	}
}

func TestProjectColoursStayClearOfStatusColours(t *testing.T) {
	const minGap = 25.0
	status := map[string]float64{}
	for _, role := range []string{"working", "needs-you", "error", "done"} {
		status[role+" light"] = hexRGB(BuiltIn.Roles[role].Light).oklch().h
		status[role+" dark"] = hexRGB(BuiltIn.Roles[role].Dark).oklch().h
	}
	for _, surface := range []string{"attention", "failed"} {
		status["mac "+surface+" light"] = hexRGB(BuiltIn.Surfaces["mac"][surface].Light).oklch().h
		status["mac "+surface+" dark"] = hexRGB(BuiltIn.Surfaces["mac"][surface].Dark).oklch().h
	}
	dark, light := webTokens(t)
	for _, token := range []string{"good", "warn", "bad"} {
		status["web "+token+" dark"] = cssHue(t, dark[token])
		status["web "+token+" light"] = cssHue(t, light[token])
	}
	for _, colour := range BuiltIn.Projects {
		for _, fill := range []string{colour.Fill.Light, colour.Fill.Dark} {
			hue := hexRGB(fill).oklch().h
			for name, statusHue := range status {
				if gap := hueGap(hue, statusHue); gap < minGap {
					t.Errorf("%s fill %s (hue %.0f) is %.0f degrees from %s (hue %.0f), want %.0f", colour.Name, fill, hue, gap, name, statusHue, minGap)
				}
			}
		}
	}
}

func TestProjectColoursStandApartFromEachOther(t *testing.T) {
	const minDistance = 0.025
	for i, a := range BuiltIn.Projects {
		for _, b := range BuiltIn.Projects[i+1:] {
			for _, shade := range []struct{ name, a, b string }{{"light", a.Fill.Light, b.Fill.Light}, {"dark", a.Fill.Dark, b.Fill.Dark}} {
				l1, a1, b1 := hexRGB(shade.a).oklch().lab()
				l2, a2, b2 := hexRGB(shade.b).oklch().lab()
				if distance := math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2)); distance < minDistance {
					t.Errorf("%s fills of %s and %s are %.3f apart, want %.3f", shade.name, a.Name, b.Name, distance, minDistance)
				}
			}
		}
	}
}

func TestProjectColoursStandApartFromTheSelectionWash(t *testing.T) {
	const minChroma, minDistance = 0.04, 0.05
	wash := regexp.MustCompile(`selectionWash: PaletteTokens\.Pair = \(0x(\w{6}), 0x(\w{6})\)`).FindStringSubmatch(readRepoFile(t, "native/macOS/DesktopPalette.swift"))
	if wash == nil {
		t.Fatal("DesktopPalette has no selectionWash pair")
	}
	hover := BuiltIn.Surfaces["web"]["hover"]
	washes := map[string][2]string{
		"mac selection": {"#" + strings.ToLower(wash[1]), "#" + strings.ToLower(wash[2])},
		"web hover":     {hover.Light, hover.Dark},
	}
	for _, colour := range BuiltIn.Projects {
		for i, fill := range []string{colour.Fill.Light, colour.Fill.Dark} {
			shown := hexRGB(fill).oklch()
			if shown.c < minChroma {
				t.Errorf("%s fill %s has chroma %.3f, want %.2f so it never reads as a grey wash", colour.Name, fill, shown.c, minChroma)
			}
			for name, pair := range washes {
				l1, a1, b1 := shown.lab()
				l2, a2, b2 := hexRGB(pair[i]).oklch().lab()
				if distance := math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2)); distance < minDistance {
					t.Errorf("%s fill %s is %.3f from the %s %s, want %.2f", colour.Name, fill, distance, name, pair[i], minDistance)
				}
			}
		}
	}
}

func TestDashboardUsesProjectColoursOnlyOnTileMarks(t *testing.T) {
	allowed := map[string]bool{"tokens.css": true, "js/project-tiles-view.js": true}
	use := regexp.MustCompile(`--project-\d|data-colour`)
	root := filepath.Join("..", "daemon", "dashboard")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if use.Match(source) && !allowed[filepath.ToSlash(rel)] {
			t.Errorf("%s uses a project colour; project colours mark a project's tile only", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	css := readRepoFile(t, "internal/daemon/dashboard/tokens.css")
	for _, m := range regexp.MustCompile(`(?m)^([^{}\n]*)\{[^}]*var\(--project-\d`).FindAllStringSubmatch(css, -1) {
		if !strings.HasPrefix(strings.TrimSpace(m[1]), ".tile-mark[data-colour=") {
			t.Errorf("tokens.css paints %q with a project colour; only .tile-mark may use one", strings.TrimSpace(m[1]))
		}
	}
}

func TestDashboardProjectColoursMatchPaletteJSON(t *testing.T) {
	dark, light := webTokens(t)
	for i, colour := range BuiltIn.Projects {
		for part, pair := range map[string]Pair{"fill": colour.Fill, "ink": colour.Ink} {
			name := "project-" + strconv.Itoa(i) + "-" + part
			if dark[name] != pair.Dark || light[name] != pair.Light {
				t.Errorf("tokens.css --%s is %s / %s, palette.json %s is %s / %s", name, dark[name], light[name], colour.Name, pair.Dark, pair.Light)
			}
		}
	}
}
