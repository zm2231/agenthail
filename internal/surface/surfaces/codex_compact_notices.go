package surfaces

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexNoticeOutputLines  = 40
	codexNoticeCommandRunes = 120
)

var codexUnservedEvents = []string{"thread_settings_applied", "additional_tools", "item_started"}

func codexBackgroundFinished(window *surface.CodexToolWindow, action codexAction) surface.TimelineItem {
	callID := ""
	if index := slices.IndexFunc(window.Background, func(command surface.CodexBackgroundCommand) bool { return command.Command == action.Command }); index >= 0 {
		callID = window.Background[index].CallID
		window.Background = slices.Delete(slices.Clone(window.Background), index, index+1)
	}
	status := "completed"
	if action.Failed {
		status = "failed"
	}
	parts := []string{codexCommandLine(action.Command)}
	if action.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *action.ExitCode))
	}
	if action.Duration >= time.Second {
		parts = append(parts, action.Duration.Round(time.Second).String())
	}
	text := strings.Join(parts, " · ")
	if output := codexOutputTail(action.Output); output != "" {
		fence := "```"
		for strings.Contains(output, fence) {
			fence += "`"
		}
		text += "\n\n" + fence + "\n" + output + "\n" + fence
	}
	return surface.TimelineItem{Kind: "event", Role: "system", Origin: surface.OriginTaskNotification, Title: "Background command", Text: text, Status: status, CallID: callID}
}

func codexCommandLine(command string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(command), "\n")
	if runes := []rune(line); len(runes) > codexNoticeCommandRunes {
		return string(runes[:codexNoticeCommandRunes-1]) + "…"
	}
	return line
}

func codexOutputTail(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) > codexNoticeOutputLines {
		lines = append([]string{"…"}, lines[len(lines)-codexNoticeOutputLines:]...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func codexGoalNotice(payload map[string]any) surface.TimelineItem {
	goal, _ := payload["goal"].(map[string]any)
	objective, _, _ := strings.Cut(strings.TrimSpace(str(goal, "objective")), "\n")
	return surface.TimelineItem{Kind: "event", Title: "Goal updated", Text: objective, Status: str(goal, "status")}
}

func codexUnknownItem(typ string) surface.TimelineItem {
	return surface.TimelineItem{Kind: "event", Title: "Codex · " + codexHumanType(typ)}
}

func codexHumanType(typ string) string {
	var words []string
	var word []rune
	for _, r := range typ {
		switch {
		case r == '_' || r == '/' || r == '.':
			words, word = append(words, string(word)), nil
		case unicode.IsUpper(r) && len(word) > 0:
			words, word = append(words, string(word)), []rune{unicode.ToLower(r)}
		default:
			word = append(word, unicode.ToLower(r))
		}
	}
	words = slices.DeleteFunc(append(words, string(word)), func(w string) bool { return w == "" })
	if len(words) == 0 {
		return "activity"
	}
	text := strings.Join(words, " ")
	return strings.ToUpper(text[:1]) + text[1:]
}
