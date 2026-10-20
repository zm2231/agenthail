package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) remoteAction(ctx context.Context, remote *remoteTarget, what string, payload map[string]any) (json.RawMessage, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return nil, err
	}
	payload["sessionId"] = remote.ID
	data, _, err := client.action(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", what, remote.label(), err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode %s answer for %s: %w", what, remote.label(), err)
	}
	return response.Result, nil
}

func (a *App) remoteMetadata(ctx context.Context, remote *remoteTarget) (map[string]json.RawMessage, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return nil, err
	}
	metadata := map[string]json.RawMessage{}
	if err := client.getJSON(ctx, "/api/v1/session-metadata", url.Values{"id": []string{remote.ID}}, &metadata); err != nil {
		return nil, fmt.Errorf("read %s: %w", remote.label(), err)
	}
	return metadata, nil
}

func (a *App) compactRemote(ctx context.Context, remote *remoteTarget) error {
	result, err := a.remoteAction(ctx, remote, "compact", map[string]any{"action": "compact"})
	if err != nil {
		return err
	}
	var receipt delivery.Receipt
	_ = json.Unmarshal(result, &receipt)
	if receipt.Evidence == surface.EvidenceQueued {
		fmt.Printf("compact queued for %s; it will run when the target is idle\n", remote.label())
		return nil
	}
	fmt.Printf("compacted %s\n", remote.label())
	return nil
}

func (a *App) modelRemote(ctx context.Context, remote *remoteTarget, name string, args []string) error {
	if name != "" {
		return a.controlRemote(ctx, remote, "model", name, args)
	}
	metadata, err := a.remoteMetadata(ctx, remote)
	if err != nil {
		return err
	}
	raw, found := metadata["model"]
	if !found {
		return fmt.Errorf("%s does not report a model", remote.label())
	}
	var current string
	_ = json.Unmarshal(raw, &current)
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"sessionId": remote.ID, "model": current})
	}
	if current != "" {
		fmt.Println(current)
	}
	return nil
}

func (a *App) goalRemote(ctx context.Context, remote *remoteTarget, positional []string, jsonOut bool) error {
	if len(positional) == 1 {
		metadata, err := a.remoteMetadata(ctx, remote)
		if err != nil {
			return err
		}
		raw, found := metadata["goal"]
		if !found {
			return fmt.Errorf("%s does not support goal management", remote.label())
		}
		var goal *surface.GoalState
		_ = json.Unmarshal(raw, &goal)
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": remote.Surface, "session": remote.ID, "goal": goal})
		}
		if goal == nil || goal.Objective == "" {
			fmt.Println("(no active goal)")
			return nil
		}
		fmt.Print(formatGoalState(goal))
		return nil
	}
	operation := positional[1]
	payload := map[string]any{}
	switch operation {
	case "clear", "pause", "resume":
		if len(positional) != 2 {
			return fmt.Errorf("usage: agenthail goal <target> %s", operation)
		}
		payload["action"] = "goal-" + operation
	case "set", "edit":
		if len(positional) < 3 {
			return fmt.Errorf("usage: agenthail goal <target> %s <objective>", operation)
		}
		payload["action"], payload["message"] = "goal-"+operation, strings.Join(positional[2:], " ")
	case "budget":
		if len(positional) != 3 {
			return fmt.Errorf("usage: agenthail goal <target> budget <tokens|clear>")
		}
		value := positional[2]
		if value == "clear" {
			value = ""
		}
		payload["action"], payload["message"] = "goal-budget", value
	default:
		return fmt.Errorf("unknown goal action %q", operation)
	}
	_, err := a.remoteAction(ctx, remote, "update the goal of", payload)
	return err
}

func (a *App) identifyRemote(ctx context.Context, remote *remoteTarget, name string) error {
	if _, err := a.remoteAction(ctx, remote, "name", map[string]any{"action": "alias", "alias": name}); err != nil {
		return err
	}
	fmt.Printf("%s%s@%s -> %s (%s)\n", remote.Host.Name, hosts.Separator, name, truncate(remote.ID, 24), remote.Surface)
	return nil
}

func (a *App) threadOperationRemote(ctx context.Context, remote *remoteTarget, operation string, positional, args []string) error {
	payload := map[string]any{}
	switch operation {
	case "fork":
		payload["action"] = "session-fork"
		payload["fork"] = surface.ForkOptions{Model: flagVal(args, "--model"), LastTurnID: flagVal(args, "--last-turn")}
		payload["alias"] = strings.TrimPrefix(flagVal(args, "--alias"), "@")
	case "queue":
		if len(positional) < 3 {
			return fmt.Errorf("native queue action is required")
		}
		request := surface.NativeQueueRequest{Action: positional[2], Message: strings.Join(positional[3:], " "), ID: flagVal(args, "--id"), ClientID: flagVal(args, "--client-id"), Cursor: flagVal(args, "--cursor")}
		if ids := flagVal(args, "--ids"); ids != "" {
			request.IDs = strings.Split(ids, ",")
		}
		payload["action"], payload["nativeQueue"] = "native-queue", request
	default:
		payload["action"] = "session-lifecycle-" + operation
	}
	result, err := a.remoteAction(ctx, remote, operation, payload)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

// queueRemote hands the message to the host's own busy queue; the daemon
// holds it if the host cannot be reached.
func (a *App) queueRemote(ctx context.Context, remote *remoteTarget, message string) error {
	result, err := a.remoteAction(ctx, remote, "queue for", map[string]any{"action": "send", "message": message, "busyDelivery": "queue"})
	if err != nil {
		return err
	}
	var receipt delivery.Receipt
	_ = json.Unmarshal(result, &receipt)
	switch {
	case receipt.Evidence == surface.EvidenceQueued:
		fmt.Printf("queued for %s: %s\n", remote.label(), receipt.Detail)
	case receipt.Evidence == surface.EvidenceUnknown:
		fmt.Printf("delivery submitted to %s; %s\n", remote.label(), receipt.Detail)
	default:
		fmt.Printf("sent to %s\n", remote.label())
	}
	return nil
}

func (a *App) historyRemote(ctx context.Context, remote *remoteTarget, limit int, jsonOut bool) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	var page struct {
		Items []struct {
			CreatedAt string `json:"createdAt"`
			Kind      string `json:"kind"`
			Target    string `json:"target"`
			Source    string `json:"source"`
			Message   string `json:"message"`
			Error     string `json:"error"`
			Evidence  string `json:"evidence"`
		} `json:"items"`
	}
	query := url.Values{"sessionId": []string{remote.ID}, "limit": []string{fmt.Sprint(limit)}}
	data, _, err := client.call(ctx, "GET", "/api/v1/history", query, nil, nil)
	if err != nil {
		return fmt.Errorf("read history of %s: %w", remote.label(), err)
	}
	if jsonOut {
		var raw struct {
			Items json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"history": raw.Items})
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return err
	}
	if len(page.Items) == 0 {
		fmt.Println("(no delivery history)")
		return nil
	}
	for _, entry := range page.Items {
		label := entry.Evidence
		if label == "" {
			label = entry.Kind
		}
		target := remote.Host.Name + hosts.Separator + entry.Target
		if entry.Source != "" {
			fmt.Printf("%s %-18s %s%s%s -> %s", entry.CreatedAt, label, remote.Host.Name, hosts.Separator, entry.Source, target)
		} else {
			fmt.Printf("%s %-18s %s", entry.CreatedAt, label, target)
		}
		if entry.Message != "" {
			fmt.Printf(" %s", truncate(strings.ReplaceAll(entry.Message, "\n", " "), 140))
		}
		if entry.Error != "" {
			fmt.Printf(" error=%s", truncate(strings.ReplaceAll(entry.Error, "\n", " "), 140))
		}
		fmt.Println()
	}
	return nil
}

// queueSessionID resolves a target for this daemon's own queue rows. A held
// send for a paired host's session is stored under that session's raw id.
func (a *App) queueSessionID(ctx context.Context, target string) (string, error) {
	session, _, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return "", err
	}
	if remote != nil {
		_, raw, _ := hosts.Split(remote.ID)
		return raw, nil
	}
	return session.ID, nil
}

// controlRemote runs a control on another host and shows the evidence that
// host reported.
func (a *App) controlRemote(ctx context.Context, remote *remoteTarget, action, value string, args []string) error {
	payload := map[string]any{"action": action, "message": value}
	if action == "model" {
		payload["model"] = value
	}
	data, err := a.remoteAction(ctx, remote, action, payload)
	if err != nil {
		return err
	}
	var result surface.ControlResult
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("decode %s answer for %s: %w", action, remote.label(), err)
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	printControl(remote.label(), &result)
	return nil
}
