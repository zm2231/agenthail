package surfaces

import (
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

var (
	claudeCommandNameTag = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	claudeCommandArgsTag = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	claudeCommandElement = regexp.MustCompile(`(?s)<command-(?:name|message|args)>.*?</command-(?:name|message|args)>`)
	claudeCommandOutput  = regexp.MustCompile(`(?s)^<local-command-(stdout|stderr)>(.*)</local-command-(?:stdout|stderr)>$`)
	claudeCommandCaveat  = regexp.MustCompile(`(?s)^<local-command-caveat>.*</local-command-caveat>$`)
)

// claudeCommandMarkup reports text that is wholly the markup Claude Code writes
// for a slash command: the invocation, its output, or the caveat before them.
func claudeCommandMarkup(text string) bool {
	text = strings.TrimSpace(text)
	return claudeCommandInvocation(text) || claudeCommandOutput.MatchString(text) || claudeCommandCaveat.MatchString(text)
}

func IsTypedHumanPrompt(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || surface.IsCodexInjectedContext(text) || claudeCommandMarkup(text) {
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

func claudeCommandInvocation(text string) bool {
	text = strings.TrimSpace(text)
	return claudeCommandNameTag.MatchString(text) && strings.TrimSpace(claudeCommandElement.ReplaceAllString(text, "")) == ""
}

// claudeCommandLine returns an invocation as it is typed: "/goal tests pass".
func claudeCommandLine(text string) string {
	name := claudeCommandNameTag.FindStringSubmatch(text)
	if name == nil {
		return ""
	}
	command := strings.TrimSpace(name[1])
	if args := claudeCommandArgsTag.FindStringSubmatch(text); args != nil && strings.TrimSpace(args[1]) != "" {
		command += " " + strings.TrimSpace(args[1])
	}
	return command
}

// claudeLocalCommandOutput returns what a slash command printed, from the
// content of the record Claude Code writes for it, and whether it printed to
// stderr.
func claudeLocalCommandOutput(content string) (string, bool) {
	content = strings.TrimSpace(content)
	stream := ""
	if match := claudeCommandOutput.FindStringSubmatch(content); match != nil {
		stream, content = match[1], match[2]
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
	if claudeCommandOutput.MatchString(text) {
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
