package cli

import (
	"fmt"
	"sort"
	"strings"
)

const (
	usageSessionsShown   = 5
	usageSessionTitleMax = 40
)

func usageSessionLine(sessions []usageSessionView) string {
	ordered := append([]usageSessionView(nil), sessions...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Working && !ordered[j].Working })
	names := []string{}
	for _, session := range ordered[:min(len(ordered), usageSessionsShown)] {
		name := truncate(session.Name, usageSessionTitleMax)
		if session.Working {
			name += " (working)"
		}
		names = append(names, name)
	}
	if more := len(ordered) - len(names); more > 0 {
		names = append(names, fmt.Sprintf("+%d more", more))
	}
	return strings.Join(names, ", ")
}
