package surface

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxRequestActions is the most buttons a notification offers for a request,
// its reply included. Apple documents only that a banner shows the first two
// actions and gives no limit for the expanded view, so this stays small.
const MaxRequestActions = 4

const maxRequestActionTitle = 40

// RequestAction is one button a notification offers for a request. ID is a
// choice its Choose accepts: "allow", "always", "deny", "o<n>" for a
// question's nth option, or "reply", which takes the person's typed text.
// Titles are only for display; a choice is resolved against the request as it
// is when the button is pressed.
type RequestAction struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Actions are the buttons that answer the request from a notification. A tool
// approval gets allow, always allow when the request offers it, deny, and a
// reply that denies it with the person's message. A question that is the
// request's only question and takes one answer gets a reply that answers it
// in the person's own words, after one button per option when they fit. A
// notification answer carries one choice or one line of text, so several
// questions, or one that takes several answers, are answered in the app and
// get none.
func (r Request) Actions() []RequestAction {
	if r.Kind != WaitingQuestion {
		actions := []RequestAction{{ID: "allow", Title: "Allow"}}
		if r.AlwaysAllow {
			actions = append(actions, RequestAction{ID: "always", Title: "Always allow"})
		}
		return append(actions, RequestAction{ID: "deny", Title: "Deny"}, RequestAction{ID: "reply", Title: "Deny with message…"})
	}
	question, ok := r.singleChoice()
	if !ok {
		return nil
	}
	actions := make([]RequestAction, 0, MaxRequestActions)
	if len(question.Options) < MaxRequestActions {
		for index, option := range question.Options {
			actions = append(actions, RequestAction{ID: "o" + strconv.Itoa(index), Title: actionTitle(option.Label)})
		}
	}
	return append(actions, RequestAction{ID: "reply", Title: "Reply…"})
}

// Choose turns a notification button's choice into a decision on this
// request. A choice the request does not offer is refused, so a button from
// an older notification never answers with something the person did not see.
// text is what the person typed for a reply, and is required for one.
func (r Request) Choose(choice, text string) (Decision, error) {
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
		if choice == "reply" {
			text = strings.TrimSpace(text)
			if text == "" {
				return Decision{}, fmt.Errorf("a reply to request %s needs text", r.ID)
			}
			if r.Kind != WaitingQuestion {
				return Decision{Behavior: "deny", Message: text}, nil
			}
			return Decision{Behavior: "allow", Answers: map[string]string{question.Question: text}}, nil
		}
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
	if question.MultiSelect || len(question.Options) == 0 {
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
