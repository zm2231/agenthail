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
		return []surface.TimelineItem{{Kind: "event", Title: "Command", Text: claudeCommandLine(text)}}
	}
	if claudeCommandOutput.MatchString(text) {
		return []surface.TimelineItem{claudeLocalCommandItem(text)}
	}
	return nil
}

func claudeLocalCommandItem(content string) surface.TimelineItem {
	output, failed := claudeLocalCommandOutput(content)
	item := surface.TimelineItem{Kind: "event", Title: "Local command", Text: output}
	if failed {
		item.Status = "error"
	}
	return item
}

// claudePromptText returns the prompt a user record holds and whether a person
// wrote it. A slash command the person typed carries origin.kind human and
// reads as typed.
func claudePromptText(originKind, text string) (string, bool) {
	if originKind == claudeOriginHuman && claudeCommandInvocation(text) {
		return claudeCommandLine(text), true
	}
	return text, isHumanTranscriptText(text)
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
