package surfaces

import (
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

var (
	claudeCommandNameTag = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	claudeCommandArgsTag = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	claudeCommandOutput  = regexp.MustCompile(`(?s)^<local-command-(stdout|stderr)>(.*)</local-command-(?:stdout|stderr)>$`)
)

// claudeCommandMarkup reports text Claude Code writes around a slash command
// it ran itself: the invocation, its output, or the caveat before them. No
// person typed it as a prompt, and no model turn starts at it.
func claudeCommandMarkup(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<command-") || strings.HasPrefix(text, "<local-command-")
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
	if name := claudeCommandNameTag.FindStringSubmatch(text); name != nil {
		command := strings.TrimSpace(name[1])
		if args := claudeCommandArgsTag.FindStringSubmatch(text); args != nil && strings.TrimSpace(args[1]) != "" {
			command += " " + strings.TrimSpace(args[1])
		}
		return []surface.TimelineItem{{Kind: "event", Title: "Command", Text: command}}
	}
	if strings.HasPrefix(text, "<local-command-stdout>") || strings.HasPrefix(text, "<local-command-stderr>") {
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

// claudeRecordIsCommand reports a user record that holds only command markup.
func claudeRecordIsCommand(record map[string]any) bool {
	if str(record, "type") != "user" {
		return false
	}
	message, _ := record["message"].(map[string]any)
	return claudeCommandMarkup(claudeMessageText(message["content"]))
}
