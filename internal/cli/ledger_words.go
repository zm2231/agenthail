package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/daemonapi"
)

var errWordsNeedDaemon = errors.New("captured words are read through the Agenthail daemon, which is not running; start it with: agenthail daemon start")

func (a *App) ledgerWords(args []string, ref string) error {
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
	if !a.daemonIsRunning() {
		return errWordsNeedDaemon
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	query := url.Values{"projectKey": {key}, "view": {"words"}, "id": {ref}, "context": {strconv.Itoa(around)}}
	data, status, err := client.call(context.Background(), http.MethodGet, "/api/v1/ledger", query, nil, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return actionError(data)
	}
	var words daemonapi.Words
	if err := json.Unmarshal(data, &words); err != nil {
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
