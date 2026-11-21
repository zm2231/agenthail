package surface

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const MaxDiagnosticBytes = 2048

func DiagnosticExcerpt(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= MaxDiagnosticBytes {
		return value
	}
	cut := value[:MaxDiagnosticBytes]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return fmt.Sprintf("%s... [truncated; %d bytes total]", strings.TrimSpace(cut), len(value))
}
