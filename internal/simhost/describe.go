package simhost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// axe copies these fields from untyped accessibility attributes, so a value
// can arrive as a string, number, bool or null depending on the element.
type axNode struct {
	Type    axText  `json:"type"`
	Label   axText  `json:"AXLabel"`
	Value   axText  `json:"AXValue"`
	ID      axText  `json:"AXUniqueId"`
	Title   axText  `json:"title"`
	Enabled *axBool `json:"enabled"`
	Frame   struct {
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	} `json:"frame"`
	Children []axNode `json:"children"`
}

type axText string

func (t *axText) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch {
	case bytes.Equal(data, []byte("null")):
		*t = ""
	case len(data) > 0 && data[0] == '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = axText(s)
	default:
		var b bytes.Buffer
		if err := json.Compact(&b, data); err != nil {
			return err
		}
		*t = axText(b.String())
	}
	return nil
}

type axBool bool

func (v *axBool) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch x := raw.(type) {
	case bool:
		*v = axBool(x)
	case float64:
		*v = x != 0
	default:
		*v = true
	}
	return nil
}

func describeLines(raw []byte) ([]string, map[string]point, error) {
	var roots []axNode
	if err := json.Unmarshal(raw, &roots); err != nil {
		return nil, nil, fmt.Errorf("read the accessibility tree: %w", err)
	}
	var lines []string
	refs := map[string]point{}
	var walk func(node axNode, depth int)
	walk = func(node axNode, depth int) {
		if line := describeNode(node); line != "" {
			ref := "e" + strconv.Itoa(len(refs)+1)
			f := node.Frame
			refs[ref] = point{X: f.X + f.Width/2, Y: f.Y + f.Height/2}
			lines = append(lines, fmt.Sprintf("%s- [ref=%s] %s @(%g,%g %gx%g)", strings.Repeat("  ", depth), ref, line, f.X, f.Y, f.Width, f.Height))
			depth++
		}
		for _, child := range node.Children {
			walk(child, depth)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return lines, refs, nil
}

func describeNode(node axNode) string {
	text := func(t axText) string {
		value := strings.TrimSpace(string(t))
		if len(value) > 200 {
			value = value[:200] + "…"
		}
		return value
	}
	label, value, id := text(node.Label), text(node.Value), text(node.ID)
	if label == "" {
		label = text(node.Title)
	}
	if label == "" && value == "" && id == "" {
		return ""
	}
	parts := []string{string(node.Type)}
	if label != "" {
		parts = append(parts, strconv.Quote(label))
	}
	if id != "" {
		parts = append(parts, "id="+strconv.Quote(id))
	}
	if value != "" {
		parts = append(parts, "value="+strconv.Quote(value))
	}
	if node.Enabled != nil && !*node.Enabled {
		parts = append(parts, "disabled")
	}
	return strings.Join(parts, " ")
}

func head(lines []string, limit int) (string, bool) {
	var b strings.Builder
	for _, line := range lines {
		if b.Len()+len(line)+1 > limit {
			return b.String(), true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), false
}
