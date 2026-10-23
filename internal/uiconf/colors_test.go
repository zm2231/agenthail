package uiconf

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextColourKeysTakeOneColourAPairOrDefault(t *testing.T) {
	parsed := Parse(strings.Join([]string{
		"color.body = #EEEEEE",
		"color.faint = #9a9aa2/#5a5a62",
		"color.faint@phone = #b0b0b8/#404048",
		"color.link = accent",
		"color.muted = accent",
		"color.error = red",
		"color.done = #12345/#000000",
	}, "\n"))
	if len(parsed.Problems) != 3 {
		t.Fatalf("problems = %+v, want accent on muted, a word and a short hex refused", parsed.Problems)
	}
	mac, _ := parsed.Chosen(Device{Class: "mac"})
	phone, _ := parsed.Chosen(Device{Class: "phone"})
	if mac["color.body"] != "#eeeeee" || mac["color.faint"] != "#9a9aa2/#5a5a62" || phone["color.faint"] != "#b0b0b8/#404048" || mac["color.link"] != "accent" || mac["color.muted"] != "default" {
		t.Fatalf("mac %v phone %v", mac, phone)
	}
}

func TestEveryServedColourMeetsItsTargetOnEverySurface(t *testing.T) {
	hostile := map[string]string{
		"theme.accent": "#1b2a6b",
		"color.body":   "#2a2a2a/#f0f0f0",
		"color.faint":  "#ffffff/#000000",
		"color.meta":   "#555555",
		"color.link":   "accent",
		"color.error":  "#ff0000",
	}
	for _, chosen := range []map[string]string{{}, hostile} {
		for _, class := range []string{"mac", "phone", "tablet", "web"} {
			for _, target := range []float64{4.5, 6} {
				colors := Colors(chosen, class, target)
				for role, color := range colors {
					if color.SystemAccent {
						continue
					}
					check := func(hex string, dark bool, target float64) {
						for _, surface := range surfaceList(surfacesFor(class), dark) {
							if got := contrast(hexRGB(hex), hexRGB(surface)); got < target {
								t.Errorf("%s %s on %s (dark %v): %.2f < %.1f", class, role, surface, dark, got, target)
							}
						}
					}
					check(color.Dark, true, target)
					check(color.Light, false, target)
					check(color.Increased.Dark, true, IncreasedContrast)
					check(color.Increased.Light, false, IncreasedContrast)
				}
			}
		}
	}
}

func TestAReadableColourIsKeptAndALiftIsTheSmallestThatReads(t *testing.T) {
	colors := Colors(map[string]string{"color.body": "#f5f5f5/#111111", "color.faint": "#505050/#c0c0c0"}, "phone", 4.5)
	if body := colors["body"]; body.Dark != "#f5f5f5" || body.Light != "#111111" || len(body.Lifted) != 0 {
		t.Fatalf("a readable colour moved: %+v", body)
	}
	faint := colors["faint"]
	if faint.Dark == "#505050" || faint.Light == "#c0c0c0" || !contains(faint.Lifted, "dark") || !contains(faint.Lifted, "light") {
		t.Fatalf("an unreadable colour was not lifted: %+v", faint)
	}
	grounds := surfaceList(surfacesFor("phone"), true)
	shown := hexRGB(faint.Dark)
	before := hexRGB("#505050").mixed(rgb{1, 1, 1}, liftedAmount(t, "#505050", faint.Dark)-0.01)
	if passes(hexRGB(before.hex()), toRGB(grounds), 4.5) || !passes(shown, toRGB(grounds), 4.5) {
		t.Fatalf("lift to %s is not the smallest step that reads", faint.Dark)
	}
}

func liftedAmount(t *testing.T, from, to string) float64 {
	t.Helper()
	a, b := hexRGB(from), hexRGB(to)
	return math.Round((b.r-a.r)/(1-a.r)*100) / 100
}

func toRGB(hexes []string) []rgb {
	out := make([]rgb, len(hexes))
	for i, hex := range hexes {
		out[i] = hexRGB(hex)
	}
	return out
}

func TestTheAccentIsLiftedOrLeftToTheDevice(t *testing.T) {
	system := Colors(map[string]string{"theme.accent": "system", "color.link": "accent"}, "mac", 4.5)
	if !system["accent"].SystemAccent || !system["link"].SystemAccent {
		t.Fatalf("system accent = %+v link %+v", system["accent"], system["link"])
	}
	chosen := Colors(map[string]string{"theme.accent": "#1b2a6b", "color.link": "accent"}, "mac", 4.5)
	if chosen["accent"].SystemAccent || chosen["link"].Dark != chosen["accent"].Dark || chosen["accent"].Dark == "#1b2a6b" {
		t.Fatalf("hex accent = %+v link %+v", chosen["accent"], chosen["link"])
	}
}

func TestContrastMatchesTheSharedVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "fixtures", "contrast-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Linear []struct {
			In, Out float64
		} `json:"linear"`
		Luminance []struct {
			In  []float64
			Out float64
		} `json:"luminance"`
		Accent []struct {
			Accent, Readable []float64
			Dark             bool
			Surfaces         [][]float64
		} `json:"accent"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-4 }
	for _, v := range vectors.Linear {
		if !near(linear(v.In), v.Out) {
			t.Errorf("linear(%v) = %v, want %v", v.In, linear(v.In), v.Out)
		}
	}
	for _, v := range vectors.Luminance {
		if got := luminance(rgb{v.In[0], v.In[1], v.In[2]}); !near(got, v.Out) {
			t.Errorf("luminance(%v) = %v, want %v", v.In, got, v.Out)
		}
	}
	for _, v := range vectors.Accent {
		grounds := make([]rgb, len(v.Surfaces))
		for i, s := range v.Surfaces {
			grounds[i] = rgb{s[0], s[1], s[2]}
		}
		got := lift(rgb{v.Accent[0], v.Accent[1], v.Accent[2]}, grounds, v.Dark, 4.5, 0.05)
		want := rgb{v.Readable[0], v.Readable[1], v.Readable[2]}
		if got.hex() != want.hex() {
			t.Errorf("readable(%v, dark %v) = %s, want %s", v.Accent, v.Dark, got.hex(), want.hex())
		}
	}
}

func TestEveryTextColourIsDocumentedAndHasACSSDefault(t *testing.T) {
	docs := ShowDefaults(true)
	css := PaletteCSS()
	for _, role := range TextRoles {
		key, ok := Lookup("color." + role)
		if !ok {
			t.Fatalf("no key for %s", role)
		}
		if !strings.Contains(docs, "color."+role) || !strings.Contains(docs, key.Doc) {
			t.Errorf("show-defaults --docs leaves out color.%s", role)
		}
		if _, builtIn := BuiltIn.Roles[role]; builtIn != strings.Contains(css, "--color-"+strings.ReplaceAll(role, ".", "-")+":") {
			t.Errorf("palette.css and palette.json disagree on %s", role)
		}
	}
	if !strings.Contains(docs, "#rrggbb/#rrggbb (dark/light)") {
		t.Errorf("docs do not say how to write a pair")
	}
}
