package surfaces

import (
	"strings"
	"unicode"

	"github.com/zm2231/agenthail/internal/surface"
)

var claudeCommandElements = []string{"name", "message", "args"}

// claudeCommandMarkup reports text that is wholly the markup Claude Code writes
// for a slash command: the invocation, its output, or the caveat before them.
func claudeCommandMarkup(text string) bool {
	text = strings.TrimSpace(text)
	_, _, output := claudeCommandOutputParts(text)
	return claudeCommandInvocation(text) || output || claudeTagWraps(text, "<local-command-caveat>", "</local-command-caveat>")
}

func IsTypedHumanPrompt(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || surface.IsCodexInjectedContext(text) || surface.IsAgenthailNotice(text) || claudeCommandMarkup(text) {
		return false
	}
	for _, prefix := range []string{"<system", "<realtime_delegation>", "<task-notification>", "<cross-session-message", "<local-command-caveat>", "<artifact-content-authored-by-others", "[Request interrupted", "[Request to", "This session is being continued from a previous conversation"} {
		if strings.HasPrefix(text, prefix) {
			return false
		}
	}
	for _, command := range []string{"compact", "model", "context", "usage", "effort", "rename"} {
		if text == "/"+command || strings.HasPrefix(text, "/"+command+" ") {
			return false
		}
	}
	return true
}

// Each element closes at the first closing tag of any of the three. String
// searches, each resumed where the last stopped, keep this linear: a pasted
// argument can run to megabytes.
func claudeCommandInvocation(text string) bool {
	text = strings.TrimSpace(text)
	if _, named := claudeTagContent(text, "<command-name>", "</command-name>"); !named {
		return false
	}
	next := [3]int{-2, -2, -2}
	for at := 0; ; {
		at = len(text) - len(strings.TrimLeftFunc(text[at:], unicode.IsSpace))
		if at == len(text) {
			return true
		}
		opened := false
		for _, element := range claudeCommandElements {
			if open := "<command-" + element + ">"; strings.HasPrefix(text[at:], open) {
				at, opened = at+len(open), true
				break
			}
		}
		if !opened {
			return false
		}
		end := -1
		for i, element := range claudeCommandElements {
			closing := "</command-" + element + ">"
			if next[i] != -1 && next[i] < at {
				if index := strings.Index(text[at:], closing); index >= 0 {
					next[i] = at + index + len(closing)
				} else {
					next[i] = -1
				}
			}
			if next[i] >= 0 && (end < 0 || next[i] < end) {
				end = next[i]
			}
		}
		if end < 0 {
			return false
		}
		at = end
	}
}

// claudeCommandLine returns an invocation as it is typed: "/goal tests pass".
func claudeCommandLine(text string) string {
	name, named := claudeTagContent(text, "<command-name>", "</command-name>")
	if !named {
		return ""
	}
	command := strings.TrimSpace(name)
	if args, ok := claudeTagContent(text, "<command-args>", "</command-args>"); ok && strings.TrimSpace(args) != "" {
		command += " " + strings.TrimSpace(args)
	}
	return command
}

func claudeTagContent(text, open, closing string) (string, bool) {
	start := strings.Index(text, open)
	if start < 0 {
		return "", false
	}
	start += len(open)
	end := strings.Index(text[start:], closing)
	if end < 0 {
		return "", false
	}
	return text[start : start+end], true
}

func claudeTagWraps(text, open, closing string) bool {
	return len(text) >= len(open)+len(closing) && strings.HasPrefix(text, open) && strings.HasSuffix(text, closing)
}

func claudeCommandOutputParts(text string) (stream, content string, ok bool) {
	for _, open := range []string{"stdout", "stderr"} {
		for _, closing := range []string{"stdout", "stderr"} {
			if openTag, closeTag := "<local-command-"+open+">", "</local-command-"+closing+">"; claudeTagWraps(text, openTag, closeTag) {
				return open, text[len(openTag) : len(text)-len(closeTag)], true
			}
		}
	}
	return "", "", false
}

// claudeLocalCommandOutput returns what a slash command printed, from the
// content of the record Claude Code writes for it, and whether it printed to
// stderr.
func claudeLocalCommandOutput(content string) (string, bool) {
	content = strings.TrimSpace(content)
	stream, output, ok := claudeCommandOutputParts(content)
	if ok {
		content = output
	}
	return strings.TrimSpace(ansiEscapeRe.ReplaceAllString(content, "")), stream == "stderr"
}

// claudeCommandItems renders command markup as events. The caveat Claude Code
// puts before a command only tells the model the command was not a request,
// so it renders nothing.
func claudeCommandItems(text string) []surface.TimelineItem {
	text = strings.TrimSpace(text)
	if claudeCommandInvocation(text) {
		return []surface.TimelineItem{{Kind: "event", Title: surface.TimelineCommand, Text: claudeCommandLine(text)}}
	}
	if _, _, output := claudeCommandOutputParts(text); output {
		return []surface.TimelineItem{claudeLocalCommandItem(text)}
	}
	return nil
}

func claudeLocalCommandItem(content string) surface.TimelineItem {
	output, failed := claudeLocalCommandOutput(content)
	item := surface.TimelineItem{Kind: "event", Title: surface.TimelineLocalCommand, Text: output}
	if failed {
		item.Status = "error"
	}
	return item
}

// claudePromptText returns the prompt a user record holds and whether a person
// wrote it. Claude Code marks what the person typed with origin.kind human; a
// slash command among it reads as typed.
func claudePromptText(originKind, text string) (string, bool) {
	if originKind != claudeOriginHuman {
		return text, isHumanTranscriptText(text) || strings.HasPrefix(strings.TrimSpace(text), "<task-notification>")
	}
	if claudeCommandInvocation(text) {
		return claudeCommandLine(text), true
	}
	return text, strings.TrimSpace(text) != ""
}

// claudeRecordIsCommand reports a user record that holds only command markup
// Claude Code wrote itself. A slash command the person typed carries
// origin.kind human and stays their prompt.
func claudeRecordIsCommand(record map[string]any) bool {
	origin, _ := record["origin"].(map[string]any)
	if str(record, "type") != "user" || str(origin, "kind") == claudeOriginHuman {
		return false
	}
	message, _ := record["message"].(map[string]any)
	return claudeCommandMarkup(claudeMessageText(message["content"]))
}
