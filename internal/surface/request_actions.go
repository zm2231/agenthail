package surface

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxRequestActions is the most buttons a notification offers for a request.
const MaxRequestActions = 4

const maxRequestActionTitle = 40

// RequestAction is one button a notification offers for a request. ID is a
// choice its Choose accepts: "allow", "always", "deny", or "o<n>" for a
// question's nth option. Titles are only for display; a choice is resolved
// against the request as it is when the button is pressed.
type RequestAction struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Actions are the buttons that answer the request from a notification. A tool
// approval gets allow, always allow when the request offers it, and deny. A
// question gets one button per option when it is the request's only question,
// takes one answer and has at most MaxRequestActions options. Anything else
// is answered in the app and gets none.
func (r Request) Actions() []RequestAction {
	if r.Kind != WaitingQuestion {
		actions := []RequestAction{{ID: "allow", Title: "Allow"}}
		if r.AlwaysAllow {
			actions = append(actions, RequestAction{ID: "always", Title: "Always allow"})
		}
		return append(actions, RequestAction{ID: "deny", Title: "Deny"})
	}
	question, ok := r.singleChoice()
	if !ok {
		return nil
	}
	actions := make([]RequestAction, 0, len(question.Options))
	for index, option := range question.Options {
		actions = append(actions, RequestAction{ID: "o" + strconv.Itoa(index), Title: actionTitle(option.Label)})
	}
	return actions
}

// Choose turns a notification button's choice into a decision on this
// request. A choice the request does not offer is refused, so a button from
// an older notification never answers with something the person did not see.
func (r Request) Choose(choice string) (Decision, error) {
	for _, action := range r.Actions() {
		if action.ID != choice {
			continue
		}
		switch choice {
		case "allow", "always":
			return Decision{Behavior: "allow", Always: choice == "always"}, nil
		case "deny":
			return Decision{Behavior: "deny"}, nil
		}
		question, _ := r.singleChoice()
		index, _ := strconv.Atoi(strings.TrimPrefix(choice, "o"))
		return Decision{Behavior: "allow", Answers: map[string]string{question.Question: question.Options[index].Label}}, nil
	}
	return Decision{}, fmt.Errorf("request %s offers no choice %q", r.ID, choice)
}

// Prompt is what the request asks, in one line for a notification.
func (r Request) Prompt() string {
	if r.Kind == WaitingQuestion && len(r.Questions) > 0 {
		return r.Questions[0].Question
	}
	for _, text := range []string{r.Title, r.DisplayName, r.Tool} {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return "Claude needs your approval"
}

func (r Request) singleChoice() (Question, bool) {
	if len(r.Questions) != 1 {
		return Question{}, false
	}
	question := r.Questions[0]
	if question.MultiSelect || len(question.Options) == 0 || len(question.Options) > MaxRequestActions {
		return Question{}, false
	}
	return question, true
}

func actionTitle(label string) string {
	label = strings.TrimSpace(label)
	if len(label) <= maxRequestActionTitle {
		return label
	}
	cut := maxRequestActionTitle - len("…")
	for cut > 0 && !utf8.RuneStart(label[cut]) {
		cut--
	}
	return label[:cut] + "…"
}
