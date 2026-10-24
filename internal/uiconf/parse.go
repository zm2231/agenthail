package uiconf

import (
	"fmt"
	"strconv"
	"strings"
)

// Problem is a line that was skipped or adjusted, with its 1-based number.
type Problem struct {
	Line    int    `json:"line"`
	Key     string `json:"key,omitempty"`
	Message string `json:"message"`
}

// ProblemsError refuses a whole file that has problems, so a write never
// commits a file the apps would have to skip lines of.
type ProblemsError struct {
	Problems []Problem
}

func (e *ProblemsError) Error() string {
	lines := make([]string, 0, len(e.Problems)+1)
	lines = append(lines, "ui.conf not written; fix these lines:")
	for _, p := range e.Problems {
		lines = append(lines, p.String())
	}
	return strings.Join(lines, "\n")
}

func (p Problem) String() string {
	return fmt.Sprintf("ui.conf:%d: %s", p.Line, p.Message)
}

type setting struct {
	line   int
	key    string
	device string
	value  string
}

// Parsed is a checked ui.conf: the settings that apply and the lines that
// did not.
type Parsed struct {
	settings []setting
	Problems []Problem
}

type line struct {
	key, device, value, comment string
	assignment                  bool
}

func splitLine(raw string) (line, string) {
	text := strings.TrimSpace(raw)
	if text == "" || strings.HasPrefix(text, "#") {
		return line{}, ""
	}
	var comment string
	if at := strings.Index(text, " # "); at >= 0 {
		text, comment = strings.TrimSpace(text[:at]), strings.TrimSpace(text[at+1:])
	}
	name, value, ok := strings.Cut(text, "=")
	if !ok {
		return line{}, "expected key = value"
	}
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	key, device, _ := strings.Cut(name, "@")
	return line{key: key, device: device, value: value, comment: comment, assignment: true}, ""
}

var deviceClasses = []string{"mac", "phone", "tablet", "web"}

func checkDevice(device string) string {
	if device == "" || contains(deviceClasses, device) {
		return ""
	}
	if name, ok := strings.CutPrefix(device, "device:"); ok && name != "" {
		return ""
	}
	return "device must be mac, phone, tablet, web or device:<name>"
}

// Parse checks every line. Unknown keys and bad values are reported and
// skipped; later lines for the same key and device win.
func Parse(text string) Parsed {
	var parsed Parsed
	for i, raw := range strings.Split(text, "\n") {
		number := i + 1
		l, problem := splitLine(raw)
		if problem != "" {
			parsed.Problems = append(parsed.Problems, Problem{Line: number, Message: problem})
			continue
		}
		if !l.assignment {
			continue
		}
		key, ok := Lookup(l.key)
		if !ok {
			parsed.Problems = append(parsed.Problems, Problem{Line: number, Key: l.key, Message: "unknown key " + l.key})
			continue
		}
		if problem := checkDevice(l.device); problem != "" {
			parsed.Problems = append(parsed.Problems, Problem{Line: number, Key: l.key, Message: problem})
			continue
		}
		value, problem := key.check(l.value)
		if problem != "" {
			parsed.Problems = append(parsed.Problems, Problem{Line: number, Key: l.key, Message: problem})
		}
		if value != "" {
			parsed.settings = append(parsed.settings, setting{line: number, key: l.key, device: l.device, value: value})
		}
	}
	return parsed
}

// Device identifies the client asking: its class (mac, phone, tablet, web)
// and its own name.
type Device struct {
	Class string
	Name  string
}

// Resolve returns every key's value for one device: key@device:<name> beats
// key@<class>, which beats key, which beats the default. background.light is
// the background to draw in light appearance.
func (p Parsed) Resolve(device Device) map[string]any {
	chosen, _ := p.Chosen(device)
	values := make(map[string]any, len(Keys)+1)
	for _, key := range Keys {
		values[key.Name] = typed(key, chosen[key.Name])
	}
	values["background.light"] = expandBackground(chosen["background"]).Light
	return values
}

// Chosen returns, for one device, each key's value as ui.conf writes it, and
// the device of every value that comes from a key@<device> line.
func (p Parsed) Chosen(device Device) (map[string]string, map[string]string) {
	rank := func(s setting) int {
		switch {
		case s.device == "":
			return 1
		case s.device == device.Class:
			return 2
		case device.Name != "" && s.device == "device:"+device.Name:
			return 3
		}
		return 0
	}
	winners := map[string]setting{}
	for _, s := range p.settings {
		r := rank(s)
		if r == 0 {
			continue
		}
		if current, ok := winners[s.key]; !ok || r >= rank(current) {
			winners[s.key] = s
		}
	}
	values := make(map[string]string, len(Keys))
	overrides := map[string]string{}
	for _, key := range Keys {
		values[key.Name] = key.Default
		if s, ok := winners[key.Name]; ok {
			values[key.Name] = s.value
			if s.device != "" {
				overrides[key.Name] = s.device
			}
		}
	}
	return values, overrides
}

func typed(key Key, value string) any {
	switch key.kind {
	case kindNumber, kindVersion:
		n, _ := strconv.ParseFloat(value, 64)
		return n
	case kindList:
		if value == "none" {
			return []string{}
		}
		return strings.Split(value, ", ")
	case kindBackground:
		return expandBackground(value).Dark
	}
	return value
}

// Assignment is one `key = value` or `key@device = value` change.
type Assignment struct {
	Key    string
	Device string
	Value  string
}

// ParseAssignment reads `key = value`, checking the key, device and value.
func ParseAssignment(text string) (Assignment, error) {
	l, problem := splitLine(text)
	if problem != "" || !l.assignment {
		return Assignment{}, fmt.Errorf("%q: expected key = value", text)
	}
	key, ok := Lookup(l.key)
	if !ok {
		return Assignment{}, fmt.Errorf("unknown key %s", l.key)
	}
	if problem := checkDevice(l.device); problem != "" {
		return Assignment{}, fmt.Errorf("%s: %s", l.key, problem)
	}
	value, problem := key.check(l.value)
	if problem != "" {
		return Assignment{}, fmt.Errorf("%s", problem)
	}
	return Assignment{Key: l.key, Device: l.device, Value: value}, nil
}

func (a Assignment) name() string {
	if a.Device == "" {
		return a.Key
	}
	return a.Key + "@" + a.Device
}

// Set writes each assignment over the last line for the same key and device,
// keeping that line's comment, or appends it. Other lines are untouched.
func Set(text string, assignments ...Assignment) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	for _, a := range assignments {
		at := lastLine(lines, a.Key, a.Device)
		entry := a.name() + " = " + a.Value
		if at < 0 {
			lines = append(lines, entry)
			continue
		}
		if l, _ := splitLine(lines[at]); l.comment != "" {
			entry += "  " + l.comment
		}
		lines[at] = entry
	}
	return strings.Join(lines, "\n") + "\n"
}

// Unset removes every line for the key and device.
func Unset(text, key, device string) string {
	var kept []string
	for _, raw := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if l, _ := splitLine(raw); l.assignment && l.key == key && l.device == device {
			continue
		}
		kept = append(kept, raw)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

func lastLine(lines []string, key, device string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if l, _ := splitLine(lines[i]); l.assignment && l.key == key && l.device == device {
			return i
		}
	}
	return -1
}

// ShowDefaults prints every key with its default, and with docs its
// documentation as a comment above it, followed by the gradient presets.
func ShowDefaults(docs bool) string {
	var b strings.Builder
	width := 0
	for _, key := range Keys {
		width = max(width, len(key.Name))
	}
	for _, key := range Keys {
		if docs {
			fmt.Fprintf(&b, "# %s\n", key.Doc)
			if key.kind == kindChoice || key.kind == kindList {
				fmt.Fprintf(&b, "# values: %s\n", strings.Join(key.choices, ", "))
			}
			if key.kind == kindTextColor {
				fmt.Fprintf(&b, "# values: %s, #rrggbb, or #rrggbb/#rrggbb (dark/light); lifted until it meets contrast.target\n", strings.Join(key.choices, ", "))
			}
			if key.kind == kindNumber {
				fmt.Fprintf(&b, "# range: %s to %s\n", formatNumber(key.min), formatNumber(key.max))
			}
		}
		fmt.Fprintf(&b, "%-*s = %s\n", width, key.Name, key.Default)
		if docs {
			b.WriteString("\n")
		}
	}
	if docs {
		b.WriteString("# Built-in pictures, usable as background = art: <name>, each drawn for dark and light:\n")
		fmt.Fprintf(&b, "#   %s\n", strings.Join(ArtNames, ", "))
		b.WriteString("# Gradient presets, usable as background = gradient: <name> or copied as a mesh (dark, then light):\n")
		for _, name := range PresetNames {
			fmt.Fprintf(&b, "#   %-7s %s\n#   %-7s %s\n", name, Presets[name].Dark, "", Presets[name].Light)
		}
	}
	return b.String()
}
