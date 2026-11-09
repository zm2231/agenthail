package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/broadcast"
)

type channelHostOutcome struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Recipients int    `json:"recipients"`
	Detail     string `json:"detail,omitempty"`
	Rule       string `json:"rule,omitempty"`
}

type channelReceipt struct {
	broadcast.Result
	Hosts       []channelHostOutcome `json:"hosts,omitempty"`
	Unreachable []string             `json:"unreachable,omitempty"`
}

func (r channelReceipt) refused() int {
	count := 0
	for _, host := range r.Hosts {
		if host.State == "refused" {
			count++
		}
	}
	return count
}

func (a *App) channelSend(ctx context.Context, name, message, fromLabel string, jsonOut bool) error {
	sourceID, err := a.sourceSessionID(ctx, fromLabel)
	if err != nil {
		return err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return fmt.Errorf("a channel send goes through the daemon, which reaches every paired host: %w", err)
	}
	payload := map[string]any{"action": "channel-send", "channel": name, "message": message}
	if sourceID != "" {
		payload["sourceSessionId"] = sourceID
	}
	data, status, err := client.actionReceipt(ctx, payload)
	if err != nil {
		return err
	}
	var receipt channelReceipt
	if status != http.StatusOK && (status != http.StatusConflict || json.Unmarshal(data, &receipt) != nil || len(receipt.Recipients) == 0 && len(receipt.Hosts) == 0) {
		return actionError(data)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if jsonOut {
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		delete(fields, "error")
		if err := emit(fields); err != nil {
			return err
		}
	} else {
		a.printChannelResult(receipt)
	}
	failed, refused := receipt.Count(broadcast.OutcomeFailed), receipt.refused()
	switch {
	case refused > 0:
		return fmt.Errorf("#%s had %d failed delivery(s) and %d host(s) refused it", receipt.Target.Label(), failed, refused)
	case failed > 0:
		return fmt.Errorf("#%s had %d failed delivery(s)", receipt.Target.Label(), failed)
	}
	return nil
}

func (a *App) printChannelResult(receipt channelReceipt) {
	for _, recipient := range receipt.Recipients {
		label := recipient.Coordinator
		if label == "" {
			label = a.sessionDisplay(recipient.SessionID)
		}
		tag := map[string]string{broadcast.OutcomeSent: "[ OK ]", broadcast.OutcomeQueued: "[QUEUE]", broadcast.OutcomeSubmitted: "[SUBMIT]", broadcast.OutcomeFailed: "[FAIL]"}[recipient.Outcome]
		if recipient.Detail != "" {
			fmt.Printf("  %s %s: %s\n", tag, label, recipient.Detail)
		} else {
			fmt.Printf("  %s %s\n", tag, label)
		}
	}
	for _, host := range receipt.Hosts {
		if host.State != "reached" {
			fmt.Printf("  [%s] host %s: %s\n", strings.ToUpper(host.State), host.Name, host.Detail)
		}
	}
	fmt.Printf("#%s: %d sent, %d queued, %d submitted, %d failed\n", receipt.Target.Label(), receipt.Count(broadcast.OutcomeSent), receipt.Count(broadcast.OutcomeQueued), receipt.Count(broadcast.OutcomeSubmitted), receipt.Count(broadcast.OutcomeFailed))
	if len(receipt.Unreachable) > 0 {
		fmt.Printf("held for unreachable host(s): %s\n", strings.Join(receipt.Unreachable, ", "))
	}
}
