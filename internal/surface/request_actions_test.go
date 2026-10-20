package surface

import (
	"strings"
	"testing"
)

func TestARequestOffersOnlyTheButtonsItCanBeAnsweredWith(t *testing.T) {
	approval := Request{ID: "r1", Kind: WaitingApproval, Tool: "Bash", Title: "Claude wants to run true"}
	if got := approval.Actions(); len(got) != 2 || got[0].ID != "allow" || got[1].ID != "deny" {
		t.Fatalf("approval actions = %+v", got)
	}
	approval.AlwaysAllow = true
	if got := approval.Actions(); len(got) != 3 || got[1] != (RequestAction{ID: "always", Title: "Always allow"}) {
		t.Fatalf("approval with a rule actions = %+v", got)
	}
	if decision, err := approval.Choose("always"); err != nil || decision.Behavior != "allow" || !decision.Always {
		t.Fatalf("always = %+v, %v", decision, err)
	}
	if decision, err := approval.Choose("deny"); err != nil || decision.Behavior != "deny" {
		t.Fatalf("deny = %+v, %v", decision, err)
	}
	if _, err := approval.Choose("o0"); err == nil {
		t.Fatal("an approval took a question's choice")
	}

	long := strings.Repeat("é", 30)
	question := Request{ID: "r2", Kind: WaitingQuestion, Questions: []Question{{Question: "Which color?", Options: []QuestionOption{{Label: "Red"}, {Label: long}}}}}
	actions := question.Actions()
	if len(actions) != 2 || actions[0] != (RequestAction{ID: "o0", Title: "Red"}) || len(actions[1].Title) > maxRequestActionTitle || !strings.HasSuffix(actions[1].Title, "…") {
		t.Fatalf("question actions = %+v", actions)
	}
	decision, err := question.Choose("o1")
	if err != nil || decision.Behavior != "allow" || decision.Answers["Which color?"] != long {
		t.Fatalf("o1 = %+v, %v", decision, err)
	}
	for _, choice := range []string{"o2", "allow", "deny", "o-1", ""} {
		if _, err := question.Choose(choice); err == nil {
			t.Fatalf("question took choice %q", choice)
		}
	}
	if question.Prompt() != "Which color?" || approval.Prompt() != "Claude wants to run true" {
		t.Fatalf("prompts = %q, %q", question.Prompt(), approval.Prompt())
	}

	for name, request := range map[string]Request{
		"two questions": {Kind: WaitingQuestion, Questions: []Question{question.Questions[0], question.Questions[0]}},
		"multi-select":  {Kind: WaitingQuestion, Questions: []Question{{Question: "Which?", MultiSelect: true, Options: []QuestionOption{{Label: "a"}}}}},
		"five options":  {Kind: WaitingQuestion, Questions: []Question{{Question: "Which?", Options: make([]QuestionOption, 5)}}},
	} {
		if actions := request.Actions(); len(actions) != 0 {
			t.Fatalf("%s offers buttons %+v", name, actions)
		}
	}
}
