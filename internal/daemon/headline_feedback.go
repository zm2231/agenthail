package daemon

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	headlineIssue = regexp.MustCompile(`(?:^|[^\w/])#\d+\b|\bPR ?\d+\b|\bpull/\d+\b`)
	headlineWork  = regexp.MustCompile(`\b[WADQ]\d+[a-z]?\b`)
	headlineRound = regexp.MustCompile(`\br\d+\b|(?i:\bround \d+\b|\b(?:first|second|third|fourth|fifth|sixth|\d+(?:st|nd|rd|th)) (?:review )?round\b)`)
	headlineHex   = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// A note, never a refusal: the agent learns what the person will see and may replace it.
func headlineFeedback(headline string) string {
	var found []string
	if match := headlineIssue.FindString(headline); match != "" {
		found = append(found, fmt.Sprintf("names a PR or issue number (%s)", strings.TrimLeft(match, " ([")))
	}
	if match := headlineWork.FindString(headline); match != "" {
		found = append(found, fmt.Sprintf("names a work item id (%s)", match))
	}
	if match := headlineRound.FindString(headline); match != "" {
		found = append(found, fmt.Sprintf("names a review round (%s)", match))
	}
	for _, match := range headlineHex.FindAllString(headline, -1) {
		if strings.ContainsAny(match, "0123456789") && strings.ContainsAny(match, "abcdef") {
			found = append(found, fmt.Sprintf("names a commit hash or id (%s)", match))
			break
		}
	}
	if length := utf8.RuneCountInString(headline); length > headlineShownRunes {
		found = append(found, fmt.Sprintf("is %d characters, so it is cut at %d", length, headlineShownRunes))
	}
	if len(found) == 0 {
		return ""
	}
	return fmt.Sprintf("The person sees: %q. It %s. Call agenthail_end_turn again with the outcome in plain words to replace it, then write your reply to the person as your final message, and call no more tools.", shownLine(headline, headlineShownRunes), strings.Join(found, ", "))
}
