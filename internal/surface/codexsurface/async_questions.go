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
		title := strings.TrimSpace(transcript.String(entry, "title"))
		if title == "" {
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
// may ask and keep working, and an answer may come from any client.
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
	answered := map[string]bool{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		item, _ := entry["item"].(map[string]any)
		switch item["type"] {
		case "userMessage":
			for _, itemID := range codexRepliedItems(codexItemText(item)) {
				ref := threadID + ":" + itemID
				if !answered[ref] {
					answered[ref] = true
					found.Answered = append(found.Answered, ref)
				}
			}
		case "agentMessage":
			if question, ok := codexAsyncQuestion(threadID, item); ok && !answered[question.Ref] {
				found.Open = append(found.Open, question)
			}
		}
	}
	slices.Reverse(found.Open)
	return found, nil
}

// codexRepliedItems are the question items a user message answers. Codex
// names each question by its tool, item and index; an older client named
// the item alone.
func codexRepliedItems(text string) []string {
	text = strings.TrimSpace(text)
	const ideContext, ideRequest = "# Context from my IDE setup:\n", "\n## My request for Codex:\n"
	if at := strings.LastIndex(text, ideRequest); at >= 0 && strings.HasPrefix(text, ideContext) {
		text = strings.TrimSpace(text[at+len(ideRequest):])
	}
	body, found := strings.CutPrefix(text, codexQuestionReplyOpen)
	if !found {
		return nil
	}
	body, _, found = strings.Cut(body, codexQuestionReplyClose)
	if !found {
		return nil
	}
	type reply struct {
		QuestionItemID string `json:"questionItemId"`
	}
	var replies []reply
	if err := json.Unmarshal([]byte(body), &replies); err != nil {
		var one reply
		if json.Unmarshal([]byte(body), &one) != nil {
			return nil
		}
		replies = []reply{one}
	}
	var items []string
	for _, reply := range replies {
		itemID := reply.QuestionItemID
		var named []any
		if json.Unmarshal([]byte(itemID), &named) == nil && len(named) == 3 && named[0] == codexAsyncQuestionTool {
			itemID, _ = named[1].(string)
		}
		if itemID != "" && !slices.Contains(items, itemID) {
			items = append(items, itemID)
		}
	}
	return items
}

// QuestionReply is the message Codex reads as the answers to the async
// question ref names: one reply per answered question, naming it by the id
// Codex gives it, with any note after the envelope.
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
	var replies []reply
	for index, question := range questions {
		answer := strings.TrimSpace(decision.Answers[question.Question])
		if answer == "" {
			continue
		}
		id, err := codexReplyJSON([]any{codexAsyncQuestionTool, itemID, index})
		if err != nil {
			return "", err
		}
		replies = append(replies, reply{Answer: answer, Question: codexReplyTitle(question.Question), QuestionItemID: id})
	}
	note := strings.TrimSpace(decision.Message)
	if len(replies) == 0 {
		if note == "" {
			return "", errors.New("answer at least one question or write a note")
		}
		return note, nil
	}
	body, err := codexReplyJSON(replies)
	if err != nil {
		return "", err
	}
	message := codexQuestionReplyOpen + "\n" + body + "\n" + codexQuestionReplyClose
	if note != "" {
		message += "\n\n" + note
	}
	return message, nil
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
