package daemon

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var headlineMarks = []struct {
	what    string
	pattern *regexp.Regexp
	keep    func(string) bool
}{
	{"a PR or issue number", regexp.MustCompile(`(?:^|[^\w/])(#\d+)\b|\b(PR ?\d+)\b|\b(pull/\d+)\b`), nil},
	{"a work item id", regexp.MustCompile(`\b([WADQ]\d+[a-z]?)\b`), nil},
	{"a review round", regexp.MustCompile(`\b(r\d+)\b|(?i:\b(round \d+)\b|\b((?:first|second|third|fourth|fifth|sixth|\d+(?:st|nd|rd|th)) (?:review )?round)\b)`), nil},
	{"a commit hash or id", regexp.MustCompile(`\b([0-9a-f]{7,40})\b`), func(text string) bool {
		return strings.ContainsAny(text, "0123456789") && strings.ContainsAny(text, "abcdef")
	}},
}

// A note, never a refusal: the agent learns what the person will see and may replace it.
func headlineFeedback(headline string) string {
	var found []string
	for _, mark := range headlineMarks {
		var texts []string
		for _, match := range mark.pattern.FindAllStringSubmatch(headline, -1) {
			if text := matchedGroup(match); text != "" && !slices.Contains(texts, text) && (mark.keep == nil || mark.keep(text)) {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			found = append(found, fmt.Sprintf("names %s (%s)", mark.what, strings.Join(texts, ", ")))
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

func matchedGroup(match []string) string {
	for _, group := range match[1:] {
		if group != "" {
			return group
		}
	}
	return ""
}
