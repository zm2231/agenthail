package ledger

import (
	"fmt"
	"strings"
)

func ReadbackInstruction(ref, projectKey string, unattached []string) string {
	return readback(fmt.Sprintf("The person's message is utterance %s in %s. Before you reply, attach each fragment of it:", ref, projectKey), ref, "Put each readback line attach prints in your reply.", unattached)
}

func QueuedReadbackInstruction(ref, projectKey string, unattached []string) string {
	return readback(fmt.Sprintf("The person's last message to you is utterance %s in %s. Attach each fragment of it now:", ref, projectKey), ref, "Tell the person each readback line attach prints.", unattached)
}

func readback(lead, ref, report string, unattached []string) string {
	text := fmt.Sprintf(`[intent ledger] %s
  agenthail ledger attach %s --as <new-ask|extends|constrains|refines|reacts|delegates|decides|conversation> --to <goal|ask|clause|project> --clause "<their words, broadest reading>"
Goals: agenthail ledger goals; new goal: agenthail ledger goal <name>. %s Never narrow what they asked; anything you leave out is a cut they must ack.`, lead, ref, report)
	if len(unattached) > 0 {
		text += "\nStill unattached from earlier: " + strings.Join(unattached, ", ")
	}
	return text
}
