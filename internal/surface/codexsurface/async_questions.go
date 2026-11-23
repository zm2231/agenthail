package codexsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

// Codex's request_user_input_async tool holds no request open: it records
// its questions on an agent message delivered "async" and lets the turn go
// on. The answer is a later user message in the envelope Codex reads as
// answers to those questions.
const (
	codexAsyncQuestionTool  = "request_user_input_async"
	codexQuestionReplyOpen  = "<send_user_message_question_reply>"
	codexQuestionReplyClose = "</send_user_message_question_reply>"
	codexQuestionTitleBytes = 512
)

func codexAsyncQuestion(threadID string, item map[string]any) (surface.ProviderQuestion, bool) {
	itemID := transcript.String(item, "id")
	if transcript.String(item, "delivery") != "async" || itemID == "" {
		return surface.ProviderQuestion{}, false
	}
	question := surface.ProviderQuestion{Ref: threadID + ":" + itemID}
	for _, raw := range asList(item["questions"]) {
		entry, _ := raw.(map[string]any)
		title := transcript.String(entry, "title")
		if strings.TrimSpace(title) == "" {
			return surface.ProviderQuestion{}, false
		}
		asked := surface.Question{Question: title}
		for _, option := range asList(entry["options"]) {
			if label, _ := option.(string); strings.TrimSpace(label) != "" {
				asked.Options = append(asked.Options, surface.QuestionOption{Label: label})
			}
		}
		if len(asked.Options) > 0 {
			first := 0
			asked.Suggested = &first
		}
		question.Questions = append(question.Questions, asked)
	}
	return question, len(question.Questions) > 0
}

// codexQuestionWindow is how many of a thread's newest items a read
// searches for async questions and their answers.
const codexQuestionWindow = 100

// codexQuestions reads the thread's newest items, across turns: the model
// may ask and keep working. A reply envelope answers the questions it names,
// one index at a time as Codex's own clients answer them. Any other message
// from the person answers every question asked in an earlier turn: Codex
// takes the turn it starts as the answer. A steer into the turn that asked
// is about something else and answers nothing.
func codexQuestions(ctx context.Context, conn codexClient, threadID string) (*surface.ProviderQuestions, error) {
	response, err := conn.Request(ctx, "thread/items/list", map[string]any{
		"threadId":      threadID,
		"limit":         codexQuestionWindow,
		"sortDirection": "desc",
	}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/items/list: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	entries, _ := result["data"].([]any)
	found := &surface.ProviderQuestions{}
	replied := map[string]codexReplied{}
	var typedIn []string
	oldestTurn := ""
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		item, _ := entry["item"].(map[string]any)
		turn := transcript.String(entry, "turnId")
		oldestTurn = turn
		switch item["type"] {
		case "userMessage":
			text := codexItemText(item)
			replies, envelope := codexReplies(text)
			if !envelope && !codexFromAnotherSender(text) && turn != "" {
				typedIn = append(typedIn, turn)
			}
			for itemID, reply := range replies {
				replied[itemID] = replied[itemID].with(reply)
			}
		case "agentMessage":
			question, ok := codexAsyncQuestion(threadID, item)
			if !ok {
				continue
			}
			if codexTypedAfterTurn(typedIn, turn) || replied[transcript.String(item, "id")].covers(len(question.Questions)) {
				found.Answered = append(found.Answered, question.Ref)
				continue
			}
			found.Open = append(found.Open, question)
		}
	}
	found.Superseded = codexTypedAfterTurn(typedIn, oldestTurn)
	slices.Reverse(found.Open)
	return found, nil
}

// codexTypedAfterTurn reports whether the person typed in a turn other than
// turn; read newest first, every such turn came after it.
func codexTypedAfterTurn(typedIn []string, turn string) bool {
	if turn == "" {
		return false
	}
	return slices.ContainsFunc(typedIn, func(typed string) bool { return typed != turn })
}

// codexFromAnotherSender reports a message Agenthail labeled as another
// session's or its own, which answers nothing the session asked the person.
func codexFromAnotherSender(text string) bool {
	return strings.HasPrefix(text, "[from ") || strings.HasPrefix(text, "[agenthail]")
}

// codexReplied is what replies answered of one question item: the indexes
// named, or the whole item when an older client named the item alone.
type codexReplied struct {
	whole   bool
	indexes map[int]bool
}

func (r codexReplied) with(other codexReplied) codexReplied {
	merged := codexReplied{whole: r.whole || other.whole, indexes: map[int]bool{}}
	for index := range r.indexes {
		merged.indexes[index] = true
	}
	for index := range other.indexes {
		merged.indexes[index] = true
	}
	return merged
}

func (r codexReplied) covers(questions int) bool {
	if r.whole {
		return true
	}
	for index := range questions {
		if !r.indexes[index] {
			return false
		}
	}
	return questions > 0
}

// codexReplies reads a user message as Codex's clients do: an envelope, on
// its own or after the IDE context prefix, answers the questions it names.
func codexReplies(text string) (map[string]codexReplied, bool) {
	text = strings.TrimSpace(text)
	const ideContext, ideRequest = "# Context from my IDE setup:\n", "\n## My request for Codex:\n"
	if at := strings.LastIndex(text, ideRequest); at >= 0 && strings.HasPrefix(text, ideContext) {
		text = strings.TrimSpace(text[at+len(ideRequest):])
	}
	body, found := strings.CutPrefix(text, codexQuestionReplyOpen)
	if !found {
		return nil, false
	}
	body, found = strings.CutSuffix(body, codexQuestionReplyClose)
	if !found {
		return nil, false
	}
	type reply struct {
		QuestionItemID string `json:"questionItemId"`
		Question       string `json:"question"`
		Answer         string `json:"answer"`
	}
	var replies []reply
	if err := json.Unmarshal([]byte(body), &replies); err != nil {
		var one reply
		if json.Unmarshal([]byte(body), &one) != nil {
			return nil, false
		}
		replies = []reply{one}
	}
	if len(replies) == 0 {
		return nil, false
	}
	answered := map[string]codexReplied{}
	for _, reply := range replies {
		var named []any
		if json.Unmarshal([]byte(reply.QuestionItemID), &named) == nil && len(named) == 3 && named[0] == codexAsyncQuestionTool {
			itemID, _ := named[1].(string)
			index, ok := named[2].(float64)
			if itemID != "" && ok {
				answered[itemID] = answered[itemID].with(codexReplied{indexes: map[int]bool{int(index): true}})
			}
			continue
		}
		if reply.QuestionItemID != "" {
			answered[reply.QuestionItemID] = answered[reply.QuestionItemID].with(codexReplied{whole: true})
		}
	}
	return answered, true
}

// QuestionReply is the message Codex reads as the answers to the async
// question ref names: one reply per question, naming it by the id Codex
// gives it, all in one envelope. Every question needs an answer, and a note
// goes with the last one, since Codex reads nothing outside the envelope.
func (c *Codex) QuestionReply(ref string, questions []surface.Question, decision surface.Decision) (string, error) {
	_, itemID, found := strings.Cut(ref, ":")
	if !found || itemID == "" {
		return "", fmt.Errorf("no Codex question %q", ref)
	}
	type reply struct {
		Answer         string `json:"answer"`
		Question       string `json:"question"`
		QuestionItemID string `json:"questionItemId"`
	}
	replies := make([]reply, 0, len(questions))
	for index, question := range questions {
		answer := strings.TrimSpace(decision.Answers[question.Question])
		if answer == "" {
			return "", fmt.Errorf("answer %q too", question.Question)
		}
		id, err := codexReplyJSON([]any{codexAsyncQuestionTool, itemID, index})
		if err != nil {
			return "", err
		}
		title := question.Question
		if question.Detail != "" {
			title = question.Detail
		}
		replies = append(replies, reply{Answer: answer, Question: codexReplyTitle(title), QuestionItemID: id})
	}
	if len(replies) == 0 {
		return "", errors.New("this question has nothing to answer")
	}
	if note := strings.TrimSpace(decision.Message); note != "" {
		replies[len(replies)-1].Answer += "\n\n" + note
	}
	body, err := codexReplyJSON(replies)
	if err != nil {
		return "", err
	}
	return codexQuestionReplyOpen + "\n" + body + "\n" + codexQuestionReplyClose, nil
}

// codexReplyTitle bounds a question's title the way Codex does before
// echoing it back: at most 512 bytes, on one line.
func codexReplyTitle(title string) string {
	if len(title) > codexQuestionTitleBytes {
		end := codexQuestionTitleBytes
		for end > 0 && !utf8.RuneStart(title[end]) {
			end--
		}
		title = title[:end]
	}
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(title)
}

func codexReplyJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}
