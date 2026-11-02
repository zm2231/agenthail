package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func parseLedgerDeadline(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if days, ok := strings.CutSuffix(value, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return time.Time{}, fmt.Errorf("--deadline %q: use a duration such as 30m, 4h or 2d", value)
		}
		return now.Add(time.Duration(n) * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("--deadline %q: use a duration such as 30m, 4h or 2d", value)
	}
	return now.Add(d), nil
}

func (a *App) ledgerQuestion(args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(strings.Join(w.positional[1:], " "))
	if text == "" || w.value("--to") == "" || w.value("--deadline") == "" || w.value("--kind") == "" {
		return fmt.Errorf(`usage: agenthail ledger question --to <goal|ask|clause|project> "<question>" --recommendation "<r>" --default "<d>" --deadline <30m|4h|2d> --kind <scope|framing|outward> [--narrows]`)
	}
	deadline, err := parseLedgerDeadline(w.value("--deadline"), time.Now())
	if err != nil {
		return err
	}
	ctx := context.Background()
	req := ledger.QuestionRequest{To: w.value("--to"), Text: text, Recommendation: w.value("--recommendation"), DefaultAnswer: w.value("--default"),
		DeadlineMs: deadline.UnixMilli(), Kind: ledger.QuestionKind(w.value("--kind")), Narrows: w.bools["--narrows"]}
	if req.ProjectKey, req.SessionID, err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	var q ledger.Question
	if err := a.ledgerPost(ctx, "/api/v1/ledger/question", req, &q); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(q)
	}
	fmt.Printf("intent ledger: +%s on %s, due %s; %s\n", q.ID, q.Target, time.UnixMilli(q.DeadlineMs).Local().Format(time.DateTime), questionFallback(q))
	return nil
}

func questionFallback(q ledger.Question) string {
	switch {
	case !q.AutoApplies() && q.Kind == ledger.QuestionOutward:
		return "unanswered, nothing is done and the person is alerted"
	case !q.AutoApplies():
		return fmt.Sprintf("unanswered, %q waits for the person's ack", q.DefaultAnswer)
	}
	return fmt.Sprintf("unanswered, %q applies as a disclosed decision", q.DefaultAnswer)
}

func sortedQuestions(board *ledger.Board, open bool) []ledger.Question {
	var out []ledger.Question
	for _, q := range board.Questions() {
		if !open || q.State == ledger.QuestionOpen {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return ledgerIDNumber(out[i].ID) < ledgerIDNumber(out[j].ID) })
	return out
}

func questionLine(q ledger.Question) string {
	line := fmt.Sprintf("%s [%s, %s] on %s: %s  recommended: %s", q.ID, q.Kind, q.State, q.Target, q.Text, q.Recommendation)
	switch {
	case q.State == ledger.QuestionOpen && q.Escalated:
		line += "  overdue, escalated to the person"
	case q.State == ledger.QuestionOpen:
		line += "  due " + time.UnixMilli(q.DeadlineMs).Local().Format(time.DateTime) + "; " + questionFallback(q)
	default:
		line += "  answer: " + q.Answer
	}
	return line
}

func (a *App) ledgerQuestions(store *ledger.Store, args []string) error {
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	board, err := store.Board(key)
	if err != nil {
		return err
	}
	questions := sortedQuestions(board, !hasFlag(args, "--all"))
	if hasFlag(args, "--json") {
		return emit(questions)
	}
	if len(questions) == 0 {
		fmt.Println("no open questions")
		return nil
	}
	for _, q := range questions {
		fmt.Println(questionLine(q))
	}
	return nil
}
