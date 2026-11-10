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

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// cmdRequests lists what a session waits on a person for.
func (a *App) cmdRequests(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail requests <target> [--json]")
	}
	ctx, cancel, err := a.timedContext(args, controlCommandTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	state, label, err := a.requestState(ctx, positional[0])
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(state)
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
	usage := fmt.Errorf(`usage: agenthail answer <target> <allow|always|deny|never|stop|"answer"> [--request <id>] [--reason "text"] [--answers '{"question":"answer"}'] [--json]`)
	if len(positional) < 1 || len(positional) > 2 || (len(positional) == 1 && flagVal(args, "--answers") == "") {
		return usage
	}
	ctx, cancel, err := a.timedContext(args, controlCommandTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	sess, _, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if err := a.controlScope(ctx, sess, remote); err != nil {
		return err
	}
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
		return emit(map[string]string{"requestId": request.ID, "outcome": outcome})
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
	ctx, cancel, err := a.timedContext(args, controlCommandTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	if len(positional) == 1 {
		state, err := a.permissionModeState(ctx, positional[0])
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return emit(map[string]string{"permissionMode": state.PermissionMode})
		}
		fmt.Println(permissionModeLabel(state.PermissionMode))
		return nil
	}
	mode := positional[1]
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if err := a.controlScope(ctx, sess, remote); err != nil {
		return err
	}
	if remote != nil {
		data, err := a.remoteAction(ctx, remote, "permission-mode", map[string]any{"action": "permission-mode", "permissionMode": mode})
		if err != nil {
			return err
		}
		var result surface.ControlResult
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("decode permission-mode answer for %s: %w", remote.Label(), err)
		}
		if hasFlag(args, "--json") {
			return emit(result)
		}
		printControl(remote.Label(), &result)
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
		a.recordHistory(registry.HistoryEntry{Kind: "permission-mode", SessionID: sess.ID, Message: mode, Result: "changed by " + daemon.CLIActor})
	}
	return a.printControlResult(sess, result, controlErr, args)
}

// A running daemon holds open requests, terminal Claude prompts included.
func (a *App) requestState(ctx context.Context, target string) (surface.RequestState, string, error) {
	sess, _, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return surface.RequestState{}, "", err
	}
	if remote != nil {
		return a.daemonRequests(ctx, remote.ID, remote.Label())
	}
	label := a.resolveDisplay(sess.ID)
	if !a.daemonIsRunning() {
		return surface.RequestState{}, "", errRequestsNeedDaemon
	}
	return a.daemonRequests(ctx, sess.ID, label)
}

var errRequestsNeedDaemon = errors.New("open requests are held by the Agenthail daemon, which is not running; start it with agenthail daemon start")

func (a *App) permissionModeState(ctx context.Context, target string) (surface.RequestState, error) {
	sess, surf, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return surface.RequestState{}, err
	}
	if remote != nil || a.daemonIsRunning() {
		state, _, err := a.requestState(ctx, target)
		return state, err
	}
	responder, ok := surf.(surface.Responder)
	if !ok || !surface.EffectiveCapabilities(surf, sess).Respond {
		return surface.RequestState{}, fmt.Errorf("%s does not take answers through Agenthail", a.resolveDisplay(sess.ID))
	}
	return responder.Requests(ctx, sess)
}

func (a *App) daemonRequests(ctx context.Context, sessionID, label string) (surface.RequestState, string, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return surface.RequestState{}, "", err
	}
	var state surface.RequestState
	if err := client.getJSON(ctx, "/api/v1/requests", url.Values{"sessionId": []string{sessionID}}, &state); err != nil {
		return surface.RequestState{}, "", fmt.Errorf("read requests of %s: %w", label, err)
	}
	return state, label, nil
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
			return "", fmt.Errorf("decode answer for %s: %w", remote.Label(), err)
		}
		return result.Outcome, nil
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return "", err
	}
	if !a.daemonIsRunning() {
		return "", errRequestsNeedDaemon
	}
	return a.respondThroughDaemon(ctx, sess.ID, requestID, decision)
}

func (a *App) respondThroughDaemon(ctx context.Context, sessionID, requestID string, decision surface.Decision) (string, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return "", err
	}
	data, status, err := client.action(ctx, map[string]any{"action": "respond", "sessionId": sessionID, "requestId": requestID, "decision": decision})
	if status == http.StatusConflict {
		return "", surface.ErrRequestStale
	}
	if err != nil {
		return "", err
	}
	var response struct {
		Result struct {
			Outcome string `json:"outcome"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("decode the answer for %s: %w", a.resolveDisplay(sessionID), err)
	}
	return response.Result.Outcome, nil
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
// always or deny, and never or stop when it offers them. A question takes deny, or its answer: a number picks that
// option, other text is the answer itself, and --answers answers several
// questions at once. allow and always are never taken as a question's answer.
// An MCP server's request takes deny, or allow with --answers holding its
// form's field values.
func requestDecision(request surface.Request, reply, answers, reason string) (surface.Decision, error) {
	if request.Kind == surface.WaitingInput {
		return inputDecision(request, reply, answers)
	}
	switch reply {
	case "deny":
		return surface.Decision{Behavior: "deny", Message: reason}, nil
	case "never", "stop":
		if request.Kind == surface.WaitingQuestion {
			return surface.Decision{}, fmt.Errorf("request %s is a question: answer with an option's number, your own text, or deny", request.ID)
		}
		if reply == "never" && !request.AlwaysDeny {
			return surface.Decision{}, fmt.Errorf("request %s offers no rule to always deny it", request.ID)
		}
		if reply == "stop" && !request.Stoppable {
			return surface.Decision{}, fmt.Errorf("request %s offers no denial that stops the turn", request.ID)
		}
		return surface.Decision{Behavior: "deny", Always: reply == "never", Stop: reply == "stop"}, nil
	case "allow", "always":
		if request.Kind == surface.WaitingQuestion {
			return surface.Decision{}, fmt.Errorf("request %s is a question: answer with an option's number, your own text, or deny", request.ID)
		}
		return surface.Decision{Behavior: "allow", Always: reply == "always"}, nil
	}
	if request.Kind != surface.WaitingQuestion {
		return surface.Decision{}, fmt.Errorf("request %s asks to run %s: answer allow, always, deny, never or stop", request.ID, request.Tool)
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

func inputDecision(request surface.Request, reply, answers string) (surface.Decision, error) {
	switch {
	case reply == "deny":
		return surface.Decision{Behavior: "deny"}, nil
	case reply != "" && reply != "allow":
		return surface.Decision{}, fmt.Errorf("request %s asks for input: answer allow, with --answers for its fields, or deny", request.ID)
	case answers == "":
		return surface.Decision{Behavior: "allow"}, nil
	}
	var content map[string]json.RawMessage
	if err := json.Unmarshal([]byte(answers), &content); err != nil {
		return surface.Decision{}, fmt.Errorf("--answers must be a JSON object of field to value: %w", err)
	}
	return surface.Decision{Behavior: "allow", Content: json.RawMessage(answers)}, nil
}

func printRequest(request surface.Request) {
	summary := request.Title
	if summary == "" {
		summary = request.Tool
	}
	if e := request.Elicitation; e != nil {
		printInputRequest(request.ID, *e)
		return
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
	if request.Kind != surface.WaitingQuestion {
		choices := []string{"allow"}
		if request.AlwaysAllow {
			choices = append(choices, "always")
		}
		choices = append(choices, "deny")
		if request.AlwaysDeny {
			choices = append(choices, "never")
		}
		if request.Stoppable {
			choices = append(choices, "stop")
		}
		fmt.Printf("  answer: %s\n", strings.Join(choices, ", "))
	}
}

func printInputRequest(id string, e surface.Elicitation) {
	fmt.Printf("%s  input  %s\n", id, e.Server)
	fmt.Printf("  %s\n", e.Message)
	if e.Mode == "url" {
		fmt.Printf("  open: %s\n", e.URL)
		fmt.Println("  answer: allow once it is open, or deny")
		return
	}
	for _, field := range e.Fields {
		kind := field.Type
		switch {
		case field.MultiSelect:
			kind = "list of " + inputChoices(field.Options)
		case len(field.Options) > 0:
			kind = "one of " + inputChoices(field.Options)
		}
		required := ""
		if field.Required {
			required = ", required"
		}
		fmt.Printf("    %s (%s%s)  %s\n", field.Name, kind, required, strings.TrimSpace(field.Title+" "+field.Description))
	}
	if len(e.Fields) == 0 {
		fmt.Println("  answer: allow or deny")
	} else {
		fmt.Println(`  answer: allow --answers '{"field":value}', or deny`)
	}
}

func inputChoices(options []surface.ElicitationOption) string {
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, strconv.Quote(option.Value))
	}
	return strings.Join(values, ", ")
}

func permissionModeLabel(mode string) string {
	if mode == "" {
		return "default (from Claude settings)"
	}
	return mode
}
