package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const cliActor = "the CLI"

// cmdRequests lists what a session waits on a person for.
func (a *App) cmdRequests(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail requests <target> [--json]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	state, label, err := a.requestState(ctx, positional[0])
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(state)
	}
	if len(state.Requests) == 0 {
		fmt.Printf("%s has no open requests\n", label)
		return nil
	}
	for _, request := range state.Requests {
		printRequest(request)
	}
	return nil
}

// cmdAnswer answers a session's oldest open request, or the one --request
// names: allow, always, deny, or for a question its answer.
func (a *App) cmdAnswer(args []string) error {
	positional := stripFlags(args)
	usage := fmt.Errorf(`usage: agenthail answer <target> <allow|always|deny|"answer"> [--request <id>] [--reason "text"] [--answers '{"question":"answer"}'] [--json]`)
	if len(positional) < 1 || len(positional) > 2 || (len(positional) == 1 && flagVal(args, "--answers") == "") {
		return usage
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	state, label, err := a.requestState(ctx, positional[0])
	if err != nil {
		return err
	}
	request, err := pickRequest(state.Requests, flagVal(args, "--request"))
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	reply := ""
	if len(positional) == 2 {
		reply = positional[1]
	}
	decision, err := requestDecision(request, reply, flagVal(args, "--answers"), flagVal(args, "--reason"))
	if err != nil {
		return err
	}
	outcome, err := a.respond(ctx, positional[0], request.ID, decision)
	if errors.Is(err, surface.ErrRequestStale) {
		return fmt.Errorf("%s: %w", label, err)
	}
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"requestId": request.ID, "outcome": outcome})
	}
	fmt.Printf("%s %s for %s\n", request.ID, outcome, label)
	return nil
}

// cmdPermissionMode reads or changes the permission mode of a session the
// Claude host runs.
func (a *App) cmdPermissionMode(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: agenthail permission-mode <target> [acceptEdits|auto|bypassPermissions|manual|dontAsk|plan] [--json] [--timeout 30s]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	if len(positional) == 1 {
		state, _, err := a.requestState(ctx, positional[0])
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"permissionMode": state.PermissionMode})
		}
		fmt.Println(permissionModeLabel(state.PermissionMode))
		return nil
	}
	mode := positional[1]
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		data, err := a.remoteAction(ctx, remote, "permission-mode", map[string]any{"action": "permission-mode", "permissionMode": mode})
		if err != nil {
			return err
		}
		var result surface.ControlResult
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("decode permission-mode answer for %s: %w", remote.label(), err)
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		printControl(remote.label(), &result)
		return nil
	}
	controller, ok := surf.(surface.Controller)
	if !ok || !surface.EffectiveCapabilities(surf, sess).PermissionMode {
		return fmt.Errorf("%s cannot change its permission mode while it runs", a.resolveDisplay(sess.ID))
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	result, controlErr := controller.Control(ctx, sess, surface.ControlRequest{Action: "permission-mode", Value: mode})
	if controlErr == nil {
		a.recordHistory(registry.HistoryEntry{Kind: "permission-mode", SessionID: sess.ID, Message: mode, Result: "changed by " + cliActor})
	}
	return a.printControlResult(sess, result, controlErr, args)
}

// requestState reads a session's open requests, from its adapter here or
// from the host that runs it.
func (a *App) requestState(ctx context.Context, target string) (surface.RequestState, string, error) {
	sess, surf, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return surface.RequestState{}, "", err
	}
	if remote != nil {
		client, err := a.daemonAPI()
		if err != nil {
			return surface.RequestState{}, "", err
		}
		var state surface.RequestState
		if err := client.getJSON(ctx, "/api/v1/requests", url.Values{"sessionId": []string{remote.ID}}, &state); err != nil {
			return surface.RequestState{}, "", fmt.Errorf("read requests of %s: %w", remote.label(), err)
		}
		return state, remote.label(), nil
	}
	responder, ok := surf.(surface.Responder)
	if !ok || !surface.EffectiveCapabilities(surf, sess).Respond {
		return surface.RequestState{}, "", fmt.Errorf("%s does not take answers through Agenthail", a.resolveDisplay(sess.ID))
	}
	state, err := responder.Requests(ctx, sess)
	return state, a.resolveDisplay(sess.ID), err
}

func (a *App) respond(ctx context.Context, target, requestID string, decision surface.Decision) (string, error) {
	sess, surf, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return "", err
	}
	if remote != nil {
		data, err := a.remoteAction(ctx, remote, "answer", map[string]any{"action": "respond", "requestId": requestID, "decision": decision})
		if err != nil {
			if strings.Contains(err.Error(), surface.ErrRequestStale.Error()) {
				return "", surface.ErrRequestStale
			}
			return "", err
		}
		var result struct {
			Outcome string `json:"outcome"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return "", fmt.Errorf("decode answer for %s: %w", remote.label(), err)
		}
		return result.Outcome, nil
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return "", err
	}
	outcome, err := surf.(surface.Responder).Respond(ctx, sess, requestID, decision)
	if err != nil {
		return "", err
	}
	a.recordHistory(registry.HistoryEntry{Kind: "request-" + outcome, SessionID: sess.ID, Message: requestID, Result: "answered by " + cliActor})
	return outcome, nil
}

func (a *App) recordHistory(entry registry.HistoryEntry) {
	if a.registry() != nil {
		_ = a.registry().RecordHistory(entry)
	}
}

func pickRequest(requests []surface.Request, id string) (surface.Request, error) {
	if len(requests) == 0 {
		return surface.Request{}, fmt.Errorf("no open requests")
	}
	if id == "" {
		return requests[0], nil
	}
	for _, request := range requests {
		if request.ID == id {
			return request, nil
		}
	}
	return surface.Request{}, fmt.Errorf("%w: no open request %s", surface.ErrRequestStale, id)
}

// requestDecision reads a person's reply to request. An approval takes allow,
// always or deny. A question takes deny, or its answer: a number picks that
// option, other text is the answer itself, and --answers answers several
// questions at once. allow and always are never taken as a question's answer.
func requestDecision(request surface.Request, reply, answers, reason string) (surface.Decision, error) {
	switch reply {
	case "deny":
		return surface.Decision{Behavior: "deny", Message: reason}, nil
	case "allow", "always":
		if request.Kind == surface.WaitingQuestion {
			return surface.Decision{}, fmt.Errorf("request %s is a question: answer with an option's number, your own text, or deny", request.ID)
		}
		return surface.Decision{Behavior: "allow", Always: reply == "always"}, nil
	}
	if request.Kind != surface.WaitingQuestion {
		return surface.Decision{}, fmt.Errorf("request %s asks to run %s: answer allow, always or deny", request.ID, request.Tool)
	}
	decision := surface.Decision{Behavior: "allow", Answers: map[string]string{}}
	if answers != "" {
		if err := json.Unmarshal([]byte(answers), &decision.Answers); err != nil {
			return surface.Decision{}, fmt.Errorf("--answers must be a JSON object of question to answer: %w", err)
		}
	}
	if reply != "" {
		if len(request.Questions) != 1 {
			return surface.Decision{}, fmt.Errorf("request %s asks %d questions: answer them with --answers", request.ID, len(request.Questions))
		}
		question := request.Questions[0]
		if index, err := strconv.Atoi(reply); err == nil && index >= 1 && index <= len(question.Options) {
			reply = question.Options[index-1].Label
		}
		decision.Answers[question.Question] = reply
	}
	return decision, nil
}

func printRequest(request surface.Request) {
	summary := request.Title
	if summary == "" {
		summary = request.Tool
	}
	fmt.Printf("%s  %s  %s\n", request.ID, request.Kind, summary)
	if request.Description != "" {
		fmt.Printf("  %s\n", request.Description)
	}
	if request.Kind != surface.WaitingQuestion && len(request.Input) > 0 {
		fmt.Printf("  input: %s\n", request.Input)
	}
	for _, question := range request.Questions {
		fmt.Printf("  %s\n", question.Question)
		for i, option := range question.Options {
			fmt.Printf("    %d. %s  %s\n", i+1, option.Label, option.Description)
		}
	}
	if request.AlwaysAllow {
		fmt.Println("  answer: allow, always or deny")
	} else if request.Kind != surface.WaitingQuestion {
		fmt.Println("  answer: allow or deny")
	}
}

func permissionModeLabel(mode string) string {
	if mode == "" {
		return "default (from Claude settings)"
	}
	return mode
}
