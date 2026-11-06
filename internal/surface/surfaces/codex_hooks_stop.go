package surfaces

import (
	"strconv"
	"strings"
)

// CodexStopHookVersion is the first Codex seen to continue a turn its Stop hook blocks.
const CodexStopHookVersion = "0.163.0"

func codexUserAgentVersion(userAgent string) string {
	_, rest, found := strings.Cut(userAgent, "/")
	if !found {
		return ""
	}
	version, _, _ := strings.Cut(rest, " ")
	return version
}

func codexAtLeast(version, minimum string) bool {
	have, ok := codexVersionParts(version)
	want, _ := codexVersionParts(minimum)
	if !ok {
		return false
	}
	for i := range want {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}

func codexVersionParts(version string) ([3]int, bool) {
	var parts [3]int
	core, _, _ := strings.Cut(version, "-")
	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return parts, false
	}
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}
