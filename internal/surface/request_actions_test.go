package surface

import (
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
	if decision, err := approval.Choose("always", ""); err != nil || decision.Behavior != "allow" || !decision.Always {
		t.Fatalf("always = %+v, %v", decision, err)
	}
	if decision, err := approval.Choose("deny", ""); err != nil || decision.Behavior != "deny" {
		t.Fatalf("deny = %+v, %v", decision, err)
	}
	if decision, err := approval.Choose("reply", "  use the staging host  "); err != nil || decision.Behavior != "deny" || decision.Message != "use the staging host" {
		t.Fatalf("reply = %+v, %v", decision, err)
	}
	if _, err := approval.Choose("reply", " "); err == nil {
		t.Fatal("an empty reply denied the request")
	}
	if _, err := approval.Choose("o0", ""); err == nil {
		t.Fatal("an approval took a question's choice")
	}

	long := strings.Repeat("é", 30)
	question := Request{ID: "r2", Kind: WaitingQuestion, Questions: []Question{{Question: "Which color?", Options: []QuestionOption{{Label: "Red"}, {Label: long}}}}}
	actions := question.Actions()
	if len(actions) != 3 || actions[0] != (RequestAction{ID: "o0", Title: "Red"}) || actions[2] != (RequestAction{ID: "reply", Title: "Reply…"}) || len(actions[1].Title) > maxRequestActionTitle || !strings.HasSuffix(actions[1].Title, "…") {
		t.Fatalf("question actions = %+v", actions)
	}
	decision, err := question.Choose("o1", "")
	if err != nil || decision.Behavior != "allow" || decision.Answers["Which color?"] != long {
		t.Fatalf("o1 = %+v, %v", decision, err)
	}
	for _, choice := range []string{"o2", "allow", "deny", "o-1", ""} {
		if _, err := question.Choose(choice, ""); err == nil {
			t.Fatalf("question took choice %q", choice)
		}
	}
	if decision, err := question.Choose("reply", "Green"); err != nil || decision.Behavior != "allow" || decision.Answers["Which color?"] != "Green" {
		t.Fatalf("reply = %+v, %v", decision, err)
	}
	if _, err := question.Choose("reply", ""); err == nil {
		t.Fatal("an empty reply answered the question")
	}
	four := Request{ID: "r4", Kind: WaitingQuestion, Questions: []Question{{Question: "Which?", Options: []QuestionOption{{Label: "a"}, {Label: "b"}, {Label: "c"}, {Label: "d"}}}}}
	if actions := four.Actions(); len(actions) != 5 || actions[3] != (RequestAction{ID: "o3", Title: "d"}) || actions[4].ID != "reply" {
		t.Fatalf("a four-option question offers %+v", actions)
	}
	many := Request{ID: "r3", Kind: WaitingQuestion, Questions: []Question{{Question: "Which?", Options: make([]QuestionOption, maxQuestionOptions+1)}}}
	if actions := many.Actions(); len(actions) != 1 || actions[0].ID != "reply" {
		t.Fatalf("a question with more options than AskUserQuestion gives offers %+v", actions)
	}
	if _, err := many.Choose("o0", ""); err == nil {
		t.Fatal("a question took an option it offered no button for")
	}
	if decision, err := many.Choose("reply", "the third"); err != nil || decision.Answers["Which?"] != "the third" {
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
		if _, err := request.Choose("reply", "Green"); err == nil {
			t.Fatalf("%s took a reply", name)
		}
	}
}

func TestAnInputRequestIsDeclinedOrAnEmptyFormAcceptedFromANotification(t *testing.T) {
	form := Request{ID: "e1", Kind: WaitingInput, Elicitation: &Elicitation{Server: "deploy", Message: "Where should this deploy?", Mode: "form", Fields: []ElicitationField{{Name: "env", Type: "string"}}}}
	if got := form.Actions(); len(got) != 1 || got[0] != (RequestAction{ID: "deny", Title: "Decline"}) {
		t.Fatalf("form actions = %+v", got)
	}
	if _, err := form.Choose("allow", ""); err == nil {
		t.Fatal("a form with fields was accepted from a notification")
	}
	if decision, err := form.Choose("deny", ""); err != nil || decision.Behavior != "deny" {
		t.Fatalf("deny = %+v, %v", decision, err)
	}
	if got := form.Prompt(); got != "Where should this deploy?" {
		t.Fatalf("prompt = %q", got)
	}
	confirm := Request{ID: "e2", Kind: WaitingInput, Elicitation: &Elicitation{Server: "deploy", Mode: "form"}}
	if got := confirm.Actions(); len(got) != 2 || got[0] != (RequestAction{ID: "allow", Title: "Accept"}) || got[1].ID != "deny" {
		t.Fatalf("empty form actions = %+v", got)
	}
	if decision, err := confirm.Choose("allow", ""); err != nil || decision.Behavior != "allow" || decision.Content != nil {
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
		if _, err := confirm.Choose(choice, "text"); err == nil {
			t.Fatalf("an input request took choice %q", choice)
		}
	}
}
