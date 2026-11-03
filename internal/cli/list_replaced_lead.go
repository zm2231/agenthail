package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// replacedLeadDetail says which coordinator name a replaced conversation held and when.
func replacedLeadDetail(tenures []registry.CoordinatorTenure) string {
	if len(tenures) == 0 {
		return ""
	}
	spans := make([]string, 0, len(tenures))
	for _, tenure := range tenures {
		until := "now"
		if !tenure.Until.IsZero() {
			until = tenure.Until.Local().Format(time.DateTime)
		}
		spans = append(spans, fmt.Sprintf("@%s %s to %s", tenure.Name, tenure.From.Local().Format(time.DateTime), until))
	}
	return "Replaced lead, was " + strings.Join(spans, ", ")
}
