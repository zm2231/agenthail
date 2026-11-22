package daemon

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// A step of the work is how the agent got there, not what changed for the
// person. Outcomes the person cares about, such as merged, released or
// installed, are not steps.
var headlineMarks = []struct {
	what    string
	pattern *regexp.Regexp
	keep    func(string) bool
	advice  string
}{
	{what: "a PR or issue number", pattern: regexp.MustCompile(`(?:^|[^\w/])(#\d+)\b|\b(PR ?\d+)\b|\b(pull/\d+)\b`)},
	{what: "a work item id", pattern: regexp.MustCompile(`\b([WADQ]\d+[a-z]?)\b`)},
	{what: "a review round", pattern: regexp.MustCompile(`\b(r\d+)\b|(?i:\b(round \d+)\b|\b((?:first|second|third|fourth|fifth|sixth|\d+(?:st|nd|rd|th)) (?:review )?round)\b)`)},
	{what: "a commit hash or id", pattern: regexp.MustCompile(`\b([0-9a-f]{7,40})\b`), keep: func(text string) bool {
		return strings.ContainsAny(text, "0123456789") && strings.ContainsAny(text, "abcdef")
	}},
	{
		what:    "a step of the work",
		pattern: regexp.MustCompile(`(?i:\b(re-?review(?:ed|ing|s)?|review(?:ed|ing|s)?|gate|rebas(?:e|ed|ing)|squash(?:ed|ing)?|pushed|pushing|before (?:the )?push|the lead)\b)|\b(NO-SHIP|SHIP)\b`),
		advice:  "If nothing changed for the person this turn, say where the work stands in their words, like 'The duplicate session fix is ready; waiting on tests'.",
	},
}

// A note, never a refusal: the agent learns what the person will see and may replace it.
func headlineFeedback(headline string) string {
	var found, advice []string
	for _, mark := range headlineMarks {
		var texts []string
		for _, match := range mark.pattern.FindAllStringSubmatch(headline, -1) {
			if text := matchedGroup(match); text != "" && !slices.Contains(texts, text) && (mark.keep == nil || mark.keep(text)) {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			found = append(found, fmt.Sprintf("names %s (%s)", mark.what, strings.Join(texts, ", ")))
			if mark.advice != "" {
				advice = append(advice, mark.advice+" ")
			}
		}
	}
	if length := utf8.RuneCountInString(headline); length > headlineShownRunes {
		found = append(found, fmt.Sprintf("is %d characters, so it is cut at %d", length, headlineShownRunes))
	}
	if len(found) == 0 {
		return ""
	}
	return fmt.Sprintf("The person sees: %q. It %s. %sCall agenthail_end_turn again with the outcome in plain words to replace it, then write your reply to the person as your final message, and call no more tools.", shownLine(headline, headlineShownRunes), strings.Join(found, ", "), strings.Join(advice, ""))
}

func matchedGroup(match []string) string {
	for _, group := range match[1:] {
		if group != "" {
			return group
		}
	}
	return ""
}
