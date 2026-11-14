package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
)

const briefUsage = "usage: agenthail brief [<project>] [--from <session>] [--json]"

type briefAnswer struct {
	Ref    string            `json:"ref"`
	Result *delivery.Receipt `json:"result"`
	Since  *struct {
		At       time.Time `json:"at"`
		Fallback bool      `json:"fallback"`
	} `json:"since"`
}

func (a *App) cmdBrief(args []string) error {
	if hasFlag(args, "--help") || hasFlag(args, "-h") {
		return errors.New(briefUsage)
	}
	positional := stripFlags(args)
	if len(positional) > 1 {
		return errors.New(briefUsage)
	}
	project := ""
	if len(positional) == 1 {
		project = positional[0]
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		found, err := a.projectOf(cwd)
		if err != nil {
			return err
		}
		project = found.ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	source, err := a.sourceSessionID(ctx, flagVal(args, "--from"))
	if err != nil {
		return err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return fmt.Errorf("a brief is asked through the daemon: %w", err)
	}
	payload := map[string]any{"action": "coordinator-brief", "project": project}
	if source != "" {
		payload["sourceSessionId"] = source
	}
	data, status, err := client.action(ctx, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return actionError(data)
	}
	var answer briefAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(answer)
	}
	switch {
	case answer.Since == nil:
		fmt.Printf("Asked %s for a brief.\n", answer.Ref)
	case answer.Since.Fallback:
		fmt.Printf("Asked %s for a brief of the last 24 hours, since the person has not written to it or its project.\n", answer.Ref)
	default:
		fmt.Printf("Asked %s for a brief of everything since %s, when the person last wrote to it or its project.\n", answer.Ref, answer.Since.At.Local().Format("2006-01-02 15:04"))
	}
	if answer.Result != nil && answer.Result.Detail != "" {
		fmt.Println(answer.Result.Detail)
	}
	return nil
}
