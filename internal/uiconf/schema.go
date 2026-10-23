// Package uiconf reads, checks and versions ui.conf, the hand-editable file
// that holds a person's interface preferences. One key per line; a bad line is
// reported with its line number and the rest of the file still applies.
package uiconf

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type kind int

const (
	kindChoice kind = iota
	kindNumber
	kindColor
	kindList
	kindBackground
	kindVersion
)

// Key is one documented setting with its default.
type Key struct {
	Name    string
	Default string
	Doc     string
	kind    kind
	choices []string
	min     float64
	max     float64
}

func choice(name, def, doc string, choices ...string) Key {
	return Key{Name: name, Default: def, Doc: doc, kind: kindChoice, choices: choices}
}

func number(name, def, doc string, min, max float64) Key {
	return Key{Name: name, Default: def, Doc: doc, kind: kindNumber, min: min, max: max}
}

func color(name, def, doc string, words ...string) Key {
	return Key{Name: name, Default: def, Doc: doc, kind: kindColor, choices: words}
}

// DefaultBackground is what fills the window until ui.conf says otherwise.
const DefaultBackground = "none"

// Keys lists every setting in the order show-defaults prints them.
var Keys = []Key{
	{Name: "version", Default: "1", Doc: "File format version.", kind: kindVersion},

	choice("theme", "system", "Light or dark appearance.", "system", "dark", "light"),
	color("theme.accent", "system", "Accent for links, focus and working state: the system accent or a colour.", "system"),

	{Name: "background", Default: DefaultBackground, Doc: "What fills the window: none, desktop, art: <name>, image: <path>, gradient: <preset>, gradient: <angle>deg, <colour> <pos>%, ..., or mesh: <cols>x<rows>, <colour>, ...", kind: kindBackground},
	choice("background.fit", "cover", "How an image fills the window.", "cover", "contain", "center", "tile"),
	choice("background.position", "center", "Which part of an image stays in view.", "center", "top", "bottom", "left", "right"),
	number("background.dim", "0", "Darkens (dark) or lightens (light) the background before anything else.", 0, 0.8),
	number("background.opacity", "1", "Background strength over the ground colour on New Session and empty views.", 0, 1),
	number("background.session-opacity", "1", "Background strength inside a session.", 0, 1),

	choice("glass", "on", "Translucent panes over the background; off uses solid surfaces.", "on", "off"),
	number("glass.blur", "30", "Blur radius in points of the background behind panes.", 0, 64),
	color("glass.tint", "ground", "Tint of panes.", "ground", "accent"),
	number("glass.opacity", "0.18", "Tint opacity of the content pane. A minimum: raised where text needs it.", 0, 1),
	number("glass.sidebar.opacity", "0.45", "Tint opacity of the sidebar.", 0, 1),
	number("glass.chrome.opacity", "0", "Tint opacity of the title bar and status strip.", 0, 1),
	number("glass.composer.opacity", "0.5", "Tint opacity of floating glass: composer, tab plates, popovers. With glass.readability auto it is at least 0.2.", 0, 1),
	choice("glass.readability", "auto", "auto raises pane tint and adds a scrim where text needs it to reach contrast.target; off uses the glass opacities exactly, so text may be harder to read. Increase Contrast always uses auto.", "auto", "off"),
	number("glass.border", "0.08", "Opacity of pane hairlines.", 0, 0.3),
	number("glass.gutter", "8", "Points between floating panes; 0 joins them edge to edge.", 0, 16),

	choice("texture", "none", "Texture over the background, never over panes or text.", "none", "scanlines", "grain"),
	number("texture.strength", "0.2", "How strongly the texture darkens.", 0, 0.6),
	number("texture.spacing", "3", "Points between scanlines.", 2, 8),

	color("scrim.tint", "auto", "Colour of the layer that keeps text readable: auto takes the background's own hue, darkened.", "auto", "black"),
	number("scrim.feather", "240", "Points over which that layer fades out around text on open areas.", 40, 320),
	number("contrast.target", "4.5", "Minimum contrast for every text role. Increase contrast raises it to 7.", 4.5, 7),

	choice("titlebar.tabs", "sessions", "Open sessions as tabs in the title bar.", "sessions", "none"),
	choice("titlebar.tabs.label", "alias-title", "What a tab shows.", "alias-title", "alias", "title"),
	choice("titlebar.tabs.preview", "on", "A sidebar click opens a preview tab that the next click replaces.", "on", "off"),
	{Name: "statusbar", Default: "usage, working, needs-you, hosts, terminal", Doc: "Items in the bottom strip, in order, or none.", kind: kindList, choices: []string{"usage", "working", "needs-you", "hosts", "terminal"}},
}

var keyIndex = func() map[string]Key {
	index := make(map[string]Key, len(Keys))
	for _, key := range Keys {
		index[key.Name] = key
	}
	return index
}()

// Lookup returns the documented key with that name.
func Lookup(name string) (Key, bool) {
	key, ok := keyIndex[name]
	return key, ok
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// check validates value for the key and returns the value to use. A number
// out of range is clamped and still reported, so the line keeps an effect.
func (k Key) check(value string) (string, string) {
	switch k.kind {
	case kindVersion:
		if value != "1" {
			return "", "version must be 1"
		}
		return value, ""
	case kindChoice:
		for _, c := range k.choices {
			if value == c {
				return value, ""
			}
		}
		return "", fmt.Sprintf("%s must be one of %s", k.Name, strings.Join(k.choices, ", "))
	case kindNumber:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return "", fmt.Sprintf("%s must be a number from %s to %s", k.Name, formatNumber(k.min), formatNumber(k.max))
		}
		if n < k.min || n > k.max {
			clamped := formatNumber(min(max(n, k.min), k.max))
			return clamped, fmt.Sprintf("%s must be from %s to %s; using %s", k.Name, formatNumber(k.min), formatNumber(k.max), clamped)
		}
		return value, ""
	case kindColor:
		for _, c := range k.choices {
			if value == c {
				return value, ""
			}
		}
		if hexColor.MatchString(value) {
			return strings.ToLower(value), ""
		}
		words := append(append([]string{}, k.choices...), "#rrggbb")
		return "", fmt.Sprintf("%s must be %s", k.Name, strings.Join(words, " or "))
	case kindList:
		if value == "none" {
			return value, ""
		}
		seen := map[string]bool{}
		var items []string
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if !contains(k.choices, item) {
				return "", fmt.Sprintf("%s items must be from %s, or none", k.Name, strings.Join(k.choices, ", "))
			}
			if !seen[item] {
				seen[item] = true
				items = append(items, item)
			}
		}
		return strings.Join(items, ", "), ""
	case kindBackground:
		return checkBackground(value)
	}
	return value, ""
}

func formatNumber(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
