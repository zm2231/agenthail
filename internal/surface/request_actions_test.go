package surface

import (
	"strconv"
	"strings"
	"testing"
)

func TestARequestOffersOnlyTheButtonsItCanBeAnsweredWith(t *testing.T) {
	approval := Request{ID: "r1", Kind: WaitingApproval, Tool: "Bash", Title: "Claude wants to run true"}
	if got := approval.Actions(); len(got) != 3 || got[0].ID != "allow" || got[1].ID != "deny" || got[2].ID != "reply" {
		t.Fatalf("approval actions = %+v", got)
	}
	approval.AlwaysAllow = true
	if got := approval.Actions(); len(got) != 4 || got[1] != (RequestAction{ID: "always", Title: "Always allow"}) {
		t.Fatalf("approval with a rule actions = %+v", got)
	}
	if decision, err := approval.Resolve(Response{Choices: []string{"always"}}); err != nil || decision.Behavior != "allow" || !decision.Always {
		t.Fatalf("always = %+v, %v", decision, err)
	}
	if decision, err := approval.Resolve(Response{Choices: []string{"deny"}}); err != nil || decision.Behavior != "deny" {
		t.Fatalf("deny = %+v, %v", decision, err)
	}
	if decision, err := approval.Resolve(Response{Choices: []string{"reply"}, Message: "  use the staging host  "}); err != nil || decision.Behavior != "deny" || decision.Message != "use the staging host" {
		t.Fatalf("reply = %+v, %v", decision, err)
	}
	if _, err := approval.Resolve(Response{Choices: []string{"reply"}, Message: " "}); err == nil {
		t.Fatal("an empty reply denied the request")
	}
	if _, err := approval.Resolve(Response{Choices: []string{"o0"}}); err == nil {
		t.Fatal("an approval took a question's choice")
	}

	long := strings.Repeat("é", 30)
	question := Request{ID: "r2", Kind: WaitingQuestion, Questions: []Question{{Question: "Which color?", Options: []QuestionOption{{Label: "Red"}, {Label: long}}}}}
	actions := question.Actions()
	if len(actions) != 3 || actions[0] != (RequestAction{ID: "o0", Title: "Red"}) || actions[2] != (RequestAction{ID: "reply", Title: "Reply…"}) || len(actions[1].Title) > maxRequestActionTitle || !strings.HasSuffix(actions[1].Title, "…") {
		t.Fatalf("question actions = %+v", actions)
	}
	decision, err := question.Resolve(Response{Choices: []string{"o1"}})
	if err != nil || decision.Behavior != "allow" || decision.Answers["Which color?"] != long {
		t.Fatalf("o1 = %+v, %v", decision, err)
	}
	if decision, err := question.Resolve(Response{Choices: []string{"deny"}, Message: "not now"}); err != nil || decision.Behavior != "deny" || decision.Message != "not now" {
		t.Fatalf("decline = %+v, %v", decision, err)
	}
	for _, choice := range []string{"o2", "allow", "always", "o-1", ""} {
		if _, err := question.Resolve(Response{Choices: []string{choice}}); err == nil {
			t.Fatalf("question took choice %q", choice)
		}
	}
	if decision, err := question.Resolve(Response{Choices: []string{"reply"}, Message: "Green"}); err != nil || decision.Behavior != "allow" || decision.Answers["Which color?"] != "Green" {
		t.Fatalf("reply = %+v, %v", decision, err)
	}
	if _, err := question.Resolve(Response{Choices: []string{"reply"}}); err == nil {
		t.Fatal("an empty reply answered the question")
	}
	four := Request{ID: "r4", Kind: WaitingQuestion, Questions: []Question{{Question: "Which?", Options: []QuestionOption{{Label: "a"}, {Label: "b"}, {Label: "c"}, {Label: "d"}}}}}
	if actions := four.Actions(); len(actions) != 5 || actions[3] != (RequestAction{ID: "o3", Title: "d"}) || actions[4].ID != "reply" {
		t.Fatalf("a four-option question offers %+v", actions)
	}
	many := Request{ID: "r3", Kind: WaitingQuestion, Questions: []Question{{Question: "Which?"}}}
	for index := range maxQuestionOptions + 1 {
		many.Questions[0].Options = append(many.Questions[0].Options, QuestionOption{Label: "option " + strconv.Itoa(index)})
	}
	if actions := many.Actions(); len(actions) != 1 || actions[0].ID != "reply" {
		t.Fatalf("a question with more options than AskUserQuestion gives offers %+v", actions)
	}
	if decision, err := many.Resolve(Response{Choices: []string{"o" + strconv.Itoa(maxQuestionOptions)}}); err != nil || decision.Answers["Which?"] != "option "+strconv.Itoa(maxQuestionOptions) {
		t.Fatalf("a pick from the card beyond the buttons = %+v, %v", decision, err)
	}
	if decision, err := many.Resolve(Response{Choices: []string{"reply"}, Message: "the third"}); err != nil || decision.Answers["Which?"] != "the third" {
		t.Fatalf("reply to many = %+v, %v", decision, err)
	}
	if question.Prompt() != "Which color?" || approval.Prompt() != "Claude wants to run true" {
		t.Fatalf("prompts = %q, %q", question.Prompt(), approval.Prompt())
	}

	for name, request := range map[string]Request{
		"two questions": {Kind: WaitingQuestion, Questions: []Question{question.Questions[0], question.Questions[0]}},
		"multi-select":  {Kind: WaitingQuestion, Questions: []Question{{Question: "Which?", MultiSelect: true, Options: []QuestionOption{{Label: "a"}}}}},
	} {
		if actions := request.Actions(); len(actions) != 0 {
			t.Fatalf("%s offers buttons %+v", name, actions)
		}
		if _, err := request.Resolve(Response{Choices: []string{"reply"}, Message: "Green"}); err == nil {
			t.Fatalf("%s took a reply", name)
		}
	}
}

func TestAnInputRequestIsDeclinedOrAnEmptyFormAcceptedFromANotification(t *testing.T) {
	form := Request{ID: "e1", Kind: WaitingInput, Elicitation: &Elicitation{Server: "deploy", Message: "Where should this deploy?", Mode: "form", Fields: []ElicitationField{{Name: "env", Type: "string"}}}}
	if got := form.Actions(); len(got) != 1 || got[0] != (RequestAction{ID: "deny", Title: "Decline"}) {
		t.Fatalf("form actions = %+v", got)
	}
	if _, err := form.Resolve(Response{Choices: []string{"allow"}}); err == nil {
		t.Fatal("a form with fields was accepted without its values")
	}
	if decision, err := form.Resolve(Response{Choices: []string{"allow"}, Content: []byte(`{"env":"prod"}`)}); err != nil || decision.Behavior != "allow" || string(decision.Content) != `{"env":"prod"}` {
		t.Fatalf("accept with values = %+v, %v", decision, err)
	}
	if decision, err := form.Resolve(Response{Choices: []string{"deny"}}); err != nil || decision.Behavior != "deny" {
		t.Fatalf("deny = %+v, %v", decision, err)
	}
	if got := form.Prompt(); got != "Where should this deploy?" {
		t.Fatalf("prompt = %q", got)
	}
	confirm := Request{ID: "e2", Kind: WaitingInput, Elicitation: &Elicitation{Server: "deploy", Mode: "form"}}
	if got := confirm.Actions(); len(got) != 2 || got[0] != (RequestAction{ID: "allow", Title: "Accept"}) || got[1].ID != "deny" {
		t.Fatalf("empty form actions = %+v", got)
	}
	if decision, err := confirm.Resolve(Response{Choices: []string{"allow"}}); err != nil || decision.Behavior != "allow" || decision.Content != nil {
		t.Fatalf("accept = %+v, %v", decision, err)
	}
	if got := confirm.Prompt(); got != "deploy needs your input" {
		t.Fatalf("prompt without a message = %q", got)
	}
	link := Request{ID: "e3", Kind: WaitingInput, Elicitation: &Elicitation{Server: "docs", Message: "Sign in", Mode: "url", URL: "https://example.com"}}
	if got := link.Actions(); len(got) != 1 || got[0].ID != "deny" {
		t.Fatalf("link actions = %+v", got)
	}
	for _, choice := range []string{"always", "reply", "o0"} {
		if _, err := confirm.Resolve(Response{Choices: []string{choice}, Message: "text"}); err == nil {
			t.Fatalf("an input request took choice %q", choice)
		}
	}
}

func TestEveryOptionHasAnIDUniqueInItsRequestAndChoicesAnswerEachQuestion(t *testing.T) {
	request := Request{ID: "r", Kind: WaitingQuestion, Questions: []Question{
		{Question: "Which color?", Options: []QuestionOption{{Label: "Red"}, {Label: "Blue"}}},
		{Question: "Which sizes?", MultiSelect: true, Options: []QuestionOption{{ID: "s", Label: "S"}, {ID: "l", Label: "L"}}},
		{Question: "Anything else?", Options: []QuestionOption{{Label: "No"}}},
	}}
	named := request.WithOptionIDs()
	if ids := []string{named.Questions[0].Options[1].ID, named.Questions[1].Options[0].ID, named.Questions[2].Options[0].ID}; strings.Join(ids, ",") != "q0o1,q1s,q2o0" {
		t.Fatalf("ids %v", ids)
	}
	if again := named.WithOptionIDs(); again.Questions[0].Options[1].ID != "q0o1" {
		t.Fatalf("naming twice changed an id: %+v", again.Questions[0].Options)
	}
	decision, err := request.Resolve(Response{Choices: []string{"q0o1", "q1s", "q1l"}, Answers: map[string]string{"Anything else?": "ship it by noon", "Which color?": "ignored"}, Message: "thanks"})
	if err != nil || decision.Behavior != "allow" || decision.Message != "thanks" || len(decision.Answers) != 3 ||
		decision.Answers["Which color?"] != "Blue" || decision.Answers["Which sizes?"] != "S, L" || decision.Answers["Anything else?"] != "ship it by noon" {
		t.Fatalf("decision %+v, %v", decision, err)
	}
	for _, choices := range [][]string{{"q0o0", "q0o1"}, {"o1"}, {"q9o0"}, {"allow"}} {
		if _, err := request.Resolve(Response{Choices: choices}); err == nil {
			t.Fatalf("choices %v answered the request", choices)
		}
	}
	if _, err := request.Resolve(Response{Choices: []string{"q0o1"}, Answers: map[string]string{"Which sizes?": "M"}}); err == nil {
		t.Fatal("a provider's question was left unanswered")
	}
	if _, err := request.Resolve(Response{Answers: map[string]string{"Which colour?": "Green"}}); err == nil {
		t.Fatal("an answer to a question the request does not ask was taken")
	}
	ask := request
	ask.Kind = WaitingAsk
	if decision, err := ask.Resolve(Response{Answers: map[string]string{"Which color?": "Green"}}); err != nil || len(decision.Answers) != 1 || decision.Answers["Which color?"] != "Green" {
		t.Fatalf("an agent's ask answered in part = %+v, %v", decision, err)
	}
}

func TestARequestThatTakesOnlyItsOptionsOffersNoReply(t *testing.T) {
	closed := Request{ID: "ask-2", Kind: WaitingAsk, OptionsOnly: true, Questions: []Question{{Question: "Delivery failed", Options: []QuestionOption{{ID: "retry", Label: "Retry"}, {ID: "dismiss", Label: "Dismiss"}}}}}
	actions := closed.Actions()
	if len(actions) != 2 || actions[0].ID != "retry" || actions[1].ID != "dismiss" {
		t.Fatalf("actions = %+v, want only the options", actions)
	}
	for _, response := range []Response{{Choices: []string{"reply"}, Message: "later"}, {Answers: map[string]string{"Delivery failed": "later"}},
		{Choices: []string{"retry"}, Message: "later"}, {Choices: []string{"retry"}, Content: []byte(`{}`)}, {Choices: []string{"retry"}, Answers: map[string]string{"Delivery failed": "x"}}} {
		if _, err := closed.Resolve(response); err == nil {
			t.Fatalf("%+v answered a request that takes only its options", response)
		}
	}
}

func TestAnAskWhoseOptionsHaveTheirOwnIDsOffersThoseIDsAsButtons(t *testing.T) {
	ask := Request{ID: "ask-1", Kind: WaitingAsk, Questions: []Question{{Question: "Delivery failed", Options: []QuestionOption{{ID: "retry", Label: "Retry"}, {ID: "dismiss", Label: "Dismiss"}}}}}
	actions := ask.Actions()
	if len(actions) != 3 || actions[0].ID != "retry" || actions[1].ID != "dismiss" || actions[2].ID != "reply" {
		t.Fatalf("actions = %+v, want the options' own ids", actions)
	}
	for _, action := range actions[:2] {
		if decision, err := ask.Resolve(Response{Choices: []string{action.ID}}); err != nil || decision.Answers["Delivery failed"] == "" {
			t.Fatalf("button %s = %+v, %v", action.ID, decision, err)
		}
	}
}
