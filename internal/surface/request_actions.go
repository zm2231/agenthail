package surface

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxQuestionOptions is the most options AskUserQuestion gives a question,
// each of which gets a button.
const maxQuestionOptions = 4

const maxRequestActionTitle = 40

// RequestAction is one button a notification offers for a request. ID is a
// choice its Resolve accepts: "allow", "always", "deny", "never" (always
// deny), "stop" (deny and end the turn), a question's option id, or "reply",
// which takes the person's typed text.
// Titles are only for display; a choice is resolved against the request as it
// is when the button is pressed.
type RequestAction struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Actions are the buttons that answer the request from a notification. A tool
// approval gets allow, always allow, always deny and deny-and-stop when the
// request offers them, deny, and a reply that denies it with the person's
// message unless the provider takes no reason with a denial. A question that is the
// request's only question and takes one answer gets a reply that answers it
// in the person's own words, after one button per option. A
// notification answer carries one choice or one line of text, so several
// questions, or one that takes several answers, are answered in the app and
// get none. An MCP server's request is declined from a notification, and a
// form with no fields, which only asks to go ahead, is also accepted there;
// one with fields or a link is answered in the app.
func (r Request) Actions() []RequestAction {
	if r.Kind == WaitingInput {
		decline := RequestAction{ID: "deny", Title: "Decline"}
		if e := r.Elicitation; e != nil && e.Mode != "url" && len(e.Fields) == 0 {
			return []RequestAction{{ID: "allow", Title: "Accept"}, decline}
		}
		return []RequestAction{decline}
	}
	if !r.asksQuestions() {
		actions := []RequestAction{{ID: "allow", Title: "Allow"}}
		if r.AlwaysAllow {
			actions = append(actions, RequestAction{ID: "always", Title: "Always allow"})
		}
		actions = append(actions, RequestAction{ID: "deny", Title: "Deny"})
		if r.AlwaysDeny {
			actions = append(actions, RequestAction{ID: "never", Title: "Always deny"})
		}
		if r.Stoppable {
			actions = append(actions, RequestAction{ID: "stop", Title: "Deny and stop"})
		}
		if r.NoReason {
			return actions
		}
		return append(actions, RequestAction{ID: "reply", Title: "Deny with message…"})
	}
	question, ok := r.WithOptionIDs().singleChoice()
	if !ok {
		return nil
	}
	actions := make([]RequestAction, 0, maxQuestionOptions+1)
	if len(question.Options) <= maxQuestionOptions || r.OptionsOnly {
		for _, option := range question.Options {
			actions = append(actions, RequestAction{ID: option.ID, Title: actionTitle(option.Label)})
		}
	}
	if r.OptionsOnly {
		return actions
	}
	return append(actions, RequestAction{ID: "reply", Title: "Reply…"})
}

// Resolve reads a client's response against this request as it is now. An
// id the request does not offer is refused, so a button from an older
// notification never answers with something the person did not see. A
// provider's questions must all be answered; an agent's ask may be answered
// in part.
func (r Request) Resolve(response Response) (Decision, error) {
	note := strings.TrimSpace(response.Message)
	if r.Kind == WaitingInput {
		switch {
		case slices.Equal(response.Choices, []string{"allow"}) && len(response.Content) == 0 && r.Elicitation != nil && len(r.Elicitation.Fields) > 0:
			return Decision{}, fmt.Errorf("accept request %s with its form's values", r.ID)
		case slices.Equal(response.Choices, []string{"allow"}):
			return Decision{Behavior: "allow", Content: response.Content}, nil
		case slices.Equal(response.Choices, []string{"deny"}):
			return Decision{Behavior: "deny"}, nil
		}
		return Decision{}, fmt.Errorf("answer request %s with allow, and its form's values, or deny", r.ID)
	}
	if r.OptionsOnly && (len(response.Answers) > 0 || note != "" || len(response.Content) > 0) {
		return Decision{}, fmt.Errorf("request %s takes only its options", r.ID)
	}
	if len(response.Choices) == 1 {
		if decision, ok, err := r.action(response.Choices[0], note); ok {
			return decision, err
		}
	}
	if !r.asksQuestions() {
		if len(response.Choices) == 1 {
			return Decision{}, fmt.Errorf("request %s offers no choice %q", r.ID, response.Choices[0])
		}
		return Decision{}, fmt.Errorf("answer request %s with one of its actions", r.ID)
	}
	answers, err := r.picked(response.Choices)
	if err != nil {
		return Decision{}, err
	}
	for question, answer := range response.Answers {
		if !slices.ContainsFunc(r.Questions, func(q Question) bool { return q.Question == question }) {
			return Decision{}, fmt.Errorf("request %s asks no question %q", r.ID, question)
		}
		if _, picked := answers[question]; !picked && strings.TrimSpace(answer) != "" {
			answers[question] = strings.TrimSpace(answer)
		}
	}
	if r.Kind == WaitingQuestion {
		for _, question := range r.Questions {
			if answers[question.Question] == "" {
				return Decision{}, fmt.Errorf("answer %q too", question.Question)
			}
		}
	}
	return Decision{Behavior: "allow", Answers: answers, Message: note}, nil
}

func (r Request) action(id, note string) (Decision, bool, error) {
	if id == "deny" && r.Kind == WaitingQuestion {
		return Decision{Behavior: "deny", Message: note}, true, nil
	}
	if _, _, option := r.WithOptionIDs().option(id); option || !slices.ContainsFunc(r.Actions(), func(action RequestAction) bool { return action.ID == id }) {
		return Decision{}, false, nil
	}
	switch id {
	case "allow", "always":
		return Decision{Behavior: "allow", Always: id == "always"}, true, nil
	case "deny", "never":
		return Decision{Behavior: "deny", Always: id == "never"}, true, nil
	case "stop":
		return Decision{Behavior: "deny", Stop: true}, true, nil
	}
	if note == "" {
		return Decision{}, true, fmt.Errorf("a reply to request %s needs text", r.ID)
	}
	if !r.asksQuestions() {
		return Decision{Behavior: "deny", Message: note}, true, nil
	}
	question, _ := r.singleChoice()
	return Decision{Behavior: "allow", Answers: map[string]string{question.Question: note}}, true, nil
}

func (r Request) picked(choices []string) (map[string]string, error) {
	answers := map[string]string{}
	named := r.WithOptionIDs()
	for _, id := range choices {
		question, label, found := named.option(id)
		if !found {
			return nil, fmt.Errorf("request %s offers no choice %q", r.ID, id)
		}
		picked, again := answers[question.Question]
		switch {
		case again && !question.MultiSelect:
			return nil, fmt.Errorf("pick one option for %q", question.Question)
		case again:
			answers[question.Question] = picked + ", " + label
		default:
			answers[question.Question] = label
		}
	}
	return answers, nil
}

func (r Request) option(id string) (Question, string, bool) {
	for _, question := range r.Questions {
		for _, option := range question.Options {
			if option.ID == id {
				return question, option.Label, true
			}
		}
	}
	return Question{}, "", false
}

// Prompt is what the request asks, in one line for a notification.
func (r Request) Prompt() string {
	if r.Kind == WaitingAsk && strings.TrimSpace(r.Title) != "" {
		return r.Title
	}
	if r.asksQuestions() && len(r.Questions) > 0 {
		return r.Questions[0].Question
	}
	if r.Kind == WaitingInput && r.Elicitation != nil {
		if message := strings.TrimSpace(r.Elicitation.Message); message != "" {
			return message
		}
		return r.Elicitation.Server + " needs your input"
	}
	for _, text := range []string{r.Title, r.DisplayName, r.Tool} {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return "Claude needs your approval"
}

func (r Request) asksQuestions() bool {
	return r.Kind == WaitingQuestion || r.Kind == WaitingAsk
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

// WithOptionIDs gives every option an id unique in the request: its own, or
// o and its place, the id its single-choice action uses; each option of a
// request with several questions is prefixed q and the question's place.
func (r Request) WithOptionIDs() Request {
	questions := make([]Question, len(r.Questions))
	for i, question := range r.Questions {
		options := make([]QuestionOption, len(question.Options))
		for j, option := range question.Options {
			if option.ID == "" {
				option.ID = "o" + strconv.Itoa(j)
			}
			if prefix := "q" + strconv.Itoa(i); len(r.Questions) > 1 && !strings.HasPrefix(option.ID, prefix) {
				option.ID = prefix + option.ID
			}
			options[j] = option
		}
		question.Options = options
		questions[i] = question
	}
	r.Questions = questions
	return r
}
