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
	"time"
)

const triggersUsage = "usage: agenthail triggers [status] | runs [--trigger <name>] [--session <id>] [--limit <n>] | test <name> | retry <run-id> | reload [--json]"

type triggerRunView struct {
	ID         int64      `json:"id"`
	Trigger    string     `json:"trigger"`
	SessionID  string     `json:"sessionId"`
	TurnID     string     `json:"turnId"`
	Test       bool       `json:"test"`
	State      string     `json:"state"`
	Attempts   int        `json:"attempts"`
	ExitCode   int        `json:"exitCode"`
	Signal     string     `json:"signal"`
	Stderr     string     `json:"stderr"`
	Error      string     `json:"error"`
	QueuedAt   time.Time  `json:"queuedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

type triggerStatusView struct {
	Path     string `json:"path"`
	Loaded   bool   `json:"loaded"`
	Error    string `json:"error"`
	Triggers []struct {
		Name    string          `json:"name"`
		Action  string          `json:"action"`
		Run     []string        `json:"run"`
		Touch   string          `json:"touch"`
		LastRun *triggerRunView `json:"lastRun"`
	} `json:"triggers"`
}

func (a *App) cmdTriggers(args []string) error {
	positional := stripFlags(args)
	sub := "status"
	if len(positional) > 0 {
		sub = positional[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	asJSON := hasFlag(args, "--json")
	switch {
	case sub == "status" && len(positional) <= 1:
		var page struct {
			Status json.RawMessage `json:"status"`
		}
		if err := client.getJSON(ctx, "/api/v1/triggers", url.Values{"limit": {"1"}}, &page); err != nil {
			return err
		}
		return printTriggerStatus(page.Status, asJSON)
	case sub == "runs" && len(positional) == 1:
		query := url.Values{}
		for flag, key := range map[string]string{"--trigger": "trigger", "--session": "sessionId", "--limit": "limit"} {
			if value := flagVal(args, flag); value != "" {
				query.Set(key, value)
			}
		}
		var page struct {
			Runs json.RawMessage `json:"runs"`
		}
		if err := client.getJSON(ctx, "/api/v1/triggers", query, &page); err != nil {
			return err
		}
		if asJSON {
			fmt.Println(string(page.Runs))
			return nil
		}
		var runs []triggerRunView
		if err := json.Unmarshal(page.Runs, &runs); err != nil {
			return err
		}
		if len(runs) == 0 {
			fmt.Println("[triggers] no runs")
		}
		for _, run := range runs {
			fmt.Println(describeTriggerRun(run))
		}
		return nil
	case sub == "reload" && len(positional) == 1:
		data, err := postTriggerAction(ctx, client, map[string]any{"action": "reload"})
		if err != nil {
			return err
		}
		return printTriggerStatus(data, asJSON)
	case sub == "test" && len(positional) == 2:
		return printTriggerAction(ctx, client, map[string]any{"action": "test", "trigger": positional[1]}, asJSON)
	case sub == "retry" && len(positional) == 2:
		id, err := strconv.ParseInt(positional[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("%q is not a run id; agenthail triggers runs lists them", positional[1])
		}
		return printTriggerAction(ctx, client, map[string]any{"action": "retry", "runId": id}, asJSON)
	}
	return errors.New(triggersUsage)
}

func postTriggerAction(ctx context.Context, client *daemonAPIClient, payload map[string]any) (json.RawMessage, error) {
	data, _, err := client.call(ctx, http.MethodPost, "/api/v1/triggers", nil, payload, nil)
	return data, err
}

func printTriggerAction(ctx context.Context, client *daemonAPIClient, payload map[string]any, asJSON bool) error {
	data, err := postTriggerAction(ctx, client, payload)
	if err != nil {
		return err
	}
	if asJSON {
		fmt.Println(string(data))
		return nil
	}
	var run triggerRunView
	if err := json.Unmarshal(data, &run); err != nil {
		return err
	}
	fmt.Printf("[triggers] queued run %d of %s; agenthail triggers runs --trigger %s shows how it went\n", run.ID, run.Trigger, run.Trigger)
	return nil
}

func printTriggerStatus(data json.RawMessage, asJSON bool) error {
	if asJSON {
		fmt.Println(string(data))
		return nil
	}
	var status triggerStatusView
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	fmt.Printf("[triggers] %s\n", status.Path)
	if !status.Loaded {
		fmt.Printf("  disabled: %s\n", status.Error)
		return nil
	}
	if len(status.Triggers) == 0 {
		fmt.Println("  none configured")
	}
	for _, trigger := range status.Triggers {
		action := "touch " + trigger.Touch
		if trigger.Action == "run" {
			action = "run " + strings.Join(trigger.Run, " ")
		}
		fmt.Printf("  %s: %s\n", trigger.Name, action)
		if trigger.LastRun != nil {
			fmt.Printf("    last: %s\n", describeTriggerRun(*trigger.LastRun))
		}
	}
	return nil
}

func describeTriggerRun(run triggerRunView) string {
	line := fmt.Sprintf("%d %s %s", run.ID, run.Trigger, run.State)
	if run.State == "failed" && run.ExitCode > 0 {
		line += fmt.Sprintf(" (exit %d)", run.ExitCode)
	}
	if run.Signal != "" {
		line += " (" + run.Signal + ")"
	}
	if run.Test {
		line += " test"
	} else if run.SessionID != "" {
		line += " session " + run.SessionID
	}
	line += " queued " + run.QueuedAt.Local().Format(time.DateTime)
	if run.Error != "" {
		line += ": " + run.Error
	} else if stderr := strings.TrimSpace(run.Stderr); stderr != "" && run.State != "succeeded" {
		if lines := strings.Split(stderr, "\n"); len(lines) > 0 {
			line += ": " + lines[len(lines)-1]
		}
	}
	return line
}
