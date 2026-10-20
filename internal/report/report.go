package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	Version     = 1
	maxLine     = 2048
	maxText     = 280
	maxCommits  = 20
	maxPaths    = 50
	openingLine = "```agenthail-report"
	fence       = "```"
)

type Report struct {
	V       int      `json:"v"`
	Status  string   `json:"status"`
	Stop    string   `json:"stop"`
	Reason  string   `json:"reason,omitempty"`
	Commits []string `json:"commits,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Next    string   `json:"next,omitempty"`
}

var ErrAbsent = errors.New("no completion report at the end of the reply")

type MalformedError struct{ Reason string }

func (e MalformedError) Error() string { return "malformed completion report: " + e.Reason }

var stops = map[string][]string{
	"done":    {"complete"},
	"blocked": {"needsInput", "needsApproval"},
	"failed":  {"gaveUp"},
	"partial": {"needsInput", "needsApproval", "gaveUp"},
}

// Parse accepts only a report the skill's jq fallback also finds, then checks
// the skill's rules. A report block anywhere but the end of text is quoted
// text, so it is ErrAbsent.
func Parse(text string) (Report, error) {
	lines := strings.Split(strings.TrimRight(text, " \t\r\n"), "\n")
	if len(lines) < 2 || strings.TrimRight(lines[len(lines)-1], " \t\r") != fence {
		return Report{}, ErrAbsent
	}
	open := -1
	for i := len(lines) - 2; i >= 0; i-- {
		if strings.HasPrefix(lines[i], fence) {
			open = i
			break
		}
	}
	if open < 0 || lines[open] != openingLine {
		return Report{}, ErrAbsent
	}
	body := lines[open+1 : len(lines)-1]
	if len(body) != 1 {
		return Report{}, MalformedError{fmt.Sprintf("the block holds %d lines; it must hold one line of JSON", len(body))}
	}
	line := body[0]
	if len(line) > maxLine {
		return Report{}, MalformedError{fmt.Sprintf("the JSON line is %d bytes; at most %d", len(line), maxLine)}
	}
	var version struct {
		V *int `json:"v"`
	}
	if err := json.Unmarshal([]byte(line), &version); err != nil {
		return Report{}, MalformedError{"not a JSON object: " + err.Error()}
	}
	if version.V == nil {
		return Report{}, MalformedError{"v is missing"}
	}
	if *version.V != Version {
		return Report{}, MalformedError{fmt.Sprintf("version %d is not supported; this Agenthail reads version %d", *version.V, Version)}
	}
	var r Report
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		return Report{}, MalformedError{err.Error()}
	}
	if err := r.validate(); err != nil {
		return Report{}, err
	}
	return r, nil
}

func (r Report) validate() error {
	allowed, ok := stops[r.Status]
	if !ok {
		return MalformedError{fmt.Sprintf("status %q is not done, partial, blocked or failed", r.Status)}
	}
	if !contains(allowed, r.Stop) {
		return MalformedError{fmt.Sprintf("stop %q does not go with status %q, which allows %s", r.Stop, r.Status, strings.Join(allowed, ", "))}
	}
	if r.Status != "done" && strings.TrimSpace(r.Reason) == "" {
		return MalformedError{fmt.Sprintf("status %q needs a reason", r.Status)}
	}
	for key, value := range map[string]string{"reason": r.Reason, "next": r.Next} {
		if n := utf8.RuneCountInString(value); n > maxText {
			return MalformedError{fmt.Sprintf("%s is %d characters; at most %d", key, n, maxText)}
		}
	}
	if err := list("commits", r.Commits, maxCommits); err != nil {
		return err
	}
	return list("paths", r.Paths, maxPaths)
}

func list(key string, values []string, most int) error {
	if len(values) > most {
		return MalformedError{fmt.Sprintf("%s lists %d entries; at most %d", key, len(values), most)}
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return MalformedError{key + " has an empty entry"}
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
