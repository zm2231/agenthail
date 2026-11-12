package simhost

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type axNode struct {
	Type    string  `json:"type"`
	Label   *string `json:"AXLabel"`
	Value   *string `json:"AXValue"`
	ID      *string `json:"AXUniqueId"`
	Title   *string `json:"title"`
	Enabled *bool   `json:"enabled"`
	Frame   struct {
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	} `json:"frame"`
	Children []axNode `json:"children"`
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
	text := func(p *string) string {
		if p == nil {
			return ""
		}
		value := strings.TrimSpace(*p)
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
	parts := []string{node.Type}
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
