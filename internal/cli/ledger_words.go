package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/ledger"
)

func (a *App) ledgerWords(store *ledger.Store, args []string, ref string) error {
	around := 0
	if raw := flagVal(args, "--context"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("--context takes a number of turns")
		}
		around = n
	}
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	words, err := daemon.ReadWords(a.registry(), store, key, ref, around)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(words)
	}
	fmt.Printf("%s to %s at %s, captured on %s\n", words.ID, words.TargetID, words.At, words.Host)
	if words.Typed == "" {
		fmt.Println("  typed: nothing, only quoted")
	} else {
		fmt.Println("  typed: " + indent(words.Typed))
	}
	for _, quoted := range words.Quoted {
		fmt.Println("  quoting: " + indent(quoted))
	}
	if line := answering(words.Context); line != "" {
		fmt.Println("  " + line)
	}
	if around == 0 {
		return nil
	}
	if len(words.Turns) == 0 {
		fmt.Println("no turns around it in this host's journal")
		return nil
	}
	by := "found by its own entry"
	if words.Anchor != "entry" {
		by = "placed by time; the session's own entry for it was not found"
	}
	fmt.Printf("turns around it (%s):\n", by)
	for _, turn := range words.Turns {
		who := "agent"
		if turn.Role == "user" {
			who = "person"
		}
		if turn.Message {
			who += ", this message"
		}
		cut := ""
		if turn.Truncated {
			cut = " [cut]"
		}
		fmt.Printf("  %s %s%s: %s\n", turn.TS, who, cut, indent(turn.Text))
	}
	return nil
}

func indent(text string) string {
	return strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n    ")
}
