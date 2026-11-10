package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const askUsage = `usage:
  agenthail ask <title> [--question <text>] --option <label>[: <what it does>]... [--recommend <n>] [--link <title>=<url>] [--json]
  agenthail ask list [--json]
  agenthail ask show <ask-id> [--json]
  agenthail ask answer <ask-id> <answer>
  agenthail ask escalate <ask-id>
  agenthail ask withdraw <ask-id>`

type askOptionArg struct {
	Label string `json:"label"`
	Does  string `json:"does,omitempty"`
}

type askLinkArg struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type askQuestionArg struct {
	Question    string         `json:"question"`
	Options     []askOptionArg `json:"options"`
	Recommended *int           `json:"recommended,omitempty"`
	Links       []askLinkArg   `json:"links,omitempty"`
}

type askListed struct {
	ID         string           `json:"id"`
	SessionID  string           `json:"sessionId"`
	Title      string           `json:"title"`
	Questions  []askQuestionOut `json:"questions"`
	RoutedTo   string           `json:"routedTo"`
	State      string           `json:"state"`
	AnsweredBy string           `json:"answeredBy"`
}

type askQuestionOut struct {
	Question    string `json:"question"`
	Recommended string `json:"recommended"`
	Options     []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Does  string `json:"does"`
	} `json:"options"`
}

func parseAsk(args []string) (string, []askQuestionArg, bool, error) {
	var title string
	var questions []askQuestionArg
	jsonOut := false
	current := func() (*askQuestionArg, error) {
		if len(questions) == 0 {
			if title == "" {
				return nil, errors.New("the title comes first")
			}
			questions = append(questions, askQuestionArg{Question: title})
		}
		return &questions[len(questions)-1], nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" {
			jsonOut = true
			continue
		}
		if !strings.HasPrefix(arg, "--") {
			if title != "" {
				return "", nil, false, fmt.Errorf("unexpected argument %q; quote the title", arg)
			}
			title = arg
			continue
		}
		if i+1 >= len(args) {
			return "", nil, false, fmt.Errorf("flag %s requires a value", arg)
		}
		value := args[i+1]
		i++
		switch arg {
		case "--question":
			if title == "" {
				return "", nil, false, errors.New("the title comes first")
			}
			questions = append(questions, askQuestionArg{Question: value})
		case "--option":
			question, err := current()
			if err != nil {
				return "", nil, false, err
			}
			label, does, _ := strings.Cut(value, ":")
			question.Options = append(question.Options, askOptionArg{Label: strings.TrimSpace(label), Does: strings.TrimSpace(does)})
		case "--recommend":
			question, err := current()
			if err != nil {
				return "", nil, false, err
			}
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > len(question.Options) {
				return "", nil, false, fmt.Errorf("--recommend is the number of an option given before it, from 1")
			}
			index := n - 1
			question.Recommended = &index
		case "--link":
			question, err := current()
			if err != nil {
				return "", nil, false, err
			}
			name, target, found := strings.Cut(value, "=")
			if !found {
				return "", nil, false, errors.New("--link is <title>=<url>")
			}
			question.Links = append(question.Links, askLinkArg{Title: strings.TrimSpace(name), URL: strings.TrimSpace(target)})
		default:
			return "", nil, false, fmt.Errorf("unknown flag %s for ask", arg)
		}
	}
	if title == "" {
		return "", nil, false, errors.New(askUsage)
	}
	if len(questions) == 0 {
		questions = append(questions, askQuestionArg{Question: title})
	}
	return title, questions, jsonOut, nil
}

func (a *App) cmdAsk(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		return errors.New(askUsage)
	}
	jsonOut := hasFlag(args, "--json")
	switch args[0] {
	case "list":
		return a.listAsks(jsonOut)
	case "show":
		if len(stripFlags(args)) != 2 {
			return errors.New(askUsage)
		}
		return a.showAsk(args[1], jsonOut)
	case "answer":
		if len(args) != 3 {
			return errors.New("usage: agenthail ask answer <ask-id> <answer>")
		}
		return a.changeAsk(map[string]any{"op": "answer", "askId": args[1], "note": args[2]}, false, "answered "+args[1])
	case "escalate", "withdraw":
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail ask %s <ask-id>", args[0])
		}
		if a.callerSession() == nil {
			return fmt.Errorf("agenthail ask %s acts for the agent session it runs in, and none is set here", args[0])
		}
		return a.changeAsk(map[string]any{"op": args[0], "askId": args[1]}, true, map[string]string{"escalate": "escalated ", "withdraw": "withdrew "}[args[0]]+args[1])
	}
	title, questions, jsonOut, err := parseAsk(args)
	if err != nil {
		return err
	}
	caller := a.callerSession()
	if caller == nil {
		return errors.New("agenthail ask raises an ask for the agent session it runs in, and none is set here")
	}
	body, err := a.askCall(http.MethodPost, nil, map[string]any{"op": "raise", "sessionId": caller.ID, "title": title, "questions": questions})
	if err != nil {
		return err
	}
	if jsonOut {
		return emit(json.RawMessage(body))
	}
	var raised askListed
	if err := json.Unmarshal(body, &raised); err != nil {
		return err
	}
	fmt.Printf("%s is open and goes to %s. End your turn; the answer arrives as a message.\n", raised.ID, raised.RoutedTo)
	return nil
}

func (a *App) changeAsk(request map[string]any, asCaller bool, done string) error {
	if caller := a.callerSession(); caller != nil {
		request["sessionId"] = caller.ID
	} else if asCaller {
		return errors.New("no agent session is set here")
	}
	if _, err := a.askCall(http.MethodPost, nil, request); err != nil {
		return err
	}
	fmt.Println(done)
	return nil
}

func (a *App) listAsks(jsonOut bool) error {
	query := url.Values{}
	if caller := a.callerSession(); caller != nil {
		query.Set("routedTo", caller.ID)
	}
	return a.printAsks(query, jsonOut)
}

func (a *App) showAsk(id string, jsonOut bool) error {
	return a.printAsks(url.Values{"id": {id}}, jsonOut)
}

func (a *App) printAsks(query url.Values, jsonOut bool) error {
	body, err := a.askCall(http.MethodGet, query, nil)
	if err != nil {
		return err
	}
	if jsonOut {
		return emit(json.RawMessage(body))
	}
	var listed struct {
		Asks []askListed `json:"asks"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return err
	}
	if len(listed.Asks) == 0 {
		fmt.Println("no asks")
		return nil
	}
	for _, ask := range listed.Asks {
		fmt.Printf("%s  %s  from %s to %s: %s\n", ask.ID, ask.State, ask.SessionID, ask.RoutedTo, ask.Title)
		for i, question := range ask.Questions {
			labels := make([]string, len(question.Options))
			for j, option := range question.Options {
				labels[j] = option.Label
				if option.Does != "" {
					labels[j] += " (" + option.Does + ")"
				}
				if option.ID == question.Recommended {
					labels[j] += " [recommended]"
				}
			}
			fmt.Printf("  %d. %s %s\n", i+1, question.Question, strings.Join(labels, " | "))
		}
	}
	return nil
}

func (a *App) askCall(method string, query url.Values, payload any) ([]byte, error) {
	ctx, cancel, err := a.deadlineContext(a.DefaultTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return nil, err
	}
	body, status, err := client.call(ctx, method, "/api/v1/asks", query, payload, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		var failure struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(body, &failure) == nil && failure.Error.Message != "" {
			return nil, errors.New(failure.Error.Message)
		}
		return nil, fmt.Errorf("the daemon answered %d", status)
	}
	return body, nil
}
