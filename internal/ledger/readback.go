package ledger

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func ReadbackInstruction(ref, projectKey string, unattached []string) string {
	return readback(fmt.Sprintf("The person's message is utterance %s in %s. Before you reply, attach each fragment of it:", ref, projectKey), ref, "Put each readback line attach prints in your reply.", unattached)
}

func QueuedReadbackInstruction(ref, projectKey string, unattached []string) string {
	return readback(fmt.Sprintf("The person's last message to you is utterance %s in %s. Attach each fragment of it now:", ref, projectKey), ref, "Tell the person each readback line attach prints.", unattached)
}

func readback(lead, ref, report string, unattached []string) string {
	text := fmt.Sprintf(`[intent ledger] %s
  agenthail ledger attach %s --to <goal|ask|clause|item|project> --clause "<their words, broadest reading>" [--as <new-ask|extends|constrains|refines|reacts|delegates|decides|conversation>]
--as is read from --to when left out; the readback says how. Goals: agenthail ledger goals; new goal: agenthail ledger goal <name>. %s Never narrow what they asked; anything you leave out is a cut they must ack.`, lead, ref, report)
	if len(unattached) > 0 {
		text += "\nStill unattached from earlier: " + strings.Join(unattached, ", ")
	}
	return text
}

const (
	checkpointWordRunes  = 400
	checkpointWordsShown = 10
)

func (a WorkAction) AttachCheckpoint() bool {
	return a == WorkAdd || a == WorkClaim || a == WorkSubmit
}

func CheckpointInstruction(projectKey string, words []UtteranceView) string {
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[intent ledger] The person's words to you in %s that are not attached yet:\n", projectKey)
	shown := words
	if len(shown) > checkpointWordsShown {
		shown = shown[:checkpointWordsShown]
	}
	for _, u := range shown {
		text := strings.Join(strings.Fields(u.Text), " ")
		if utf8.RuneCountInString(text) > checkpointWordRunes {
			text = string([]rune(text)[:checkpointWordRunes]) + "..."
		}
		fmt.Fprintf(&b, "  %s: %s\n", u.ID, text)
	}
	if more := len(words) - len(shown); more > 0 {
		fmt.Fprintf(&b, "  and %d more; agenthail ledger --all lists them\n", more)
	}
	b.WriteString(`Attach them in one call, one fragment per intent, separated by --and:
  agenthail ledger attach <utterance> --to <goal|ask|clause|item|project> --clause "<their words, broadest reading>" [--as <new-ask|extends|constrains|refines|reacts|delegates|decides|conversation>] --and <utterance> ...
Never narrow what they asked; anything you leave out is a cut they must ack.`)
	return b.String()
}
