package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/systemone"
)

const (
	turnJudgeConfigKey = "turnJudge"
	turnJudgeQueue     = 64
	turnJudgeWorkers   = 2
	turnJudgeDeadline  = 15 * time.Second
	turnJudgeReplyRune = 6000
	turnJudgeSentences = 2
)

const (
	judgePromise   = "The `reply` is an AI agent's last message of its turn; after it, the agent stops until someone writes to it. Does it say the agent itself is starting or continuing work right now, so stopping here leaves that work undone?"
	judgeNeeds     = "Does the `full_reply` ask the person a question or need a decision or action from them before work can continue?"
	judgeNeedsAt   = "Does the `reply` ask the person a question or need a decision or action from them before work can continue?"
	judgeSituation = "What is the agent's situation at the end of the `reply`?"
)

var promiseQuestions = map[string]systemone.Question{
	"promise": {Type: systemone.TypeNoul, Instructions: judgePromise},
}

var judgeQuestions = map[string]systemone.Question{
	"needs":      {Type: systemone.TypeNoul, Instructions: judgeNeeds},
	"needs_tail": {Type: systemone.TypeNoul, Instructions: judgeNeedsAt},
	"state": {Type: systemone.TypeChoice, Instructions: judgeSituation, Criteria: map[string]any{
		"done":           "the work it was asked for is finished",
		"waiting_person": "it is waiting for the person to answer or decide",
		"waiting_work":   "it is waiting on tests, a build, a review or another agent",
		"continuing":     "it says it is still doing the work itself",
	}},
}

type judgeState struct {
	Reply     string `json:"reply"`
	FullReply string `json:"full_reply,omitempty"`
}

type judgeJob struct {
	judgment registry.TurnJudgment
	reply    string
}

type turnJudge struct {
	jobs chan judgeJob
	http *http.Client

	mu        sync.Mutex
	since     time.Time
	lastError string
	failures  int
	dropped   int
}

type turnJudgeHealth struct {
	Model        string    `json:"model,omitempty"`
	PromiseModel string    `json:"promiseModel,omitempty"`
	Since        time.Time `json:"since,omitzero"`
	Error        string    `json:"error,omitempty"`
	Failures     int       `json:"failures"`
	Dropped      int       `json:"dropped"`
}

func newTurnJudge() *turnJudge {
	return &turnJudge{jobs: make(chan judgeJob, turnJudgeQueue)}
}

type turnJudgeConfig struct {
	systemone.Config
	promise systemone.Config
}

func loadTurnJudgeConfig() (turnJudgeConfig, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return turnJudgeConfig{}, false, err
	}
	config, ok, err := systemone.LoadConfig(home, turnJudgeConfigKey)
	if err != nil || !ok {
		return turnJudgeConfig{}, ok, err
	}
	all, err := surface.ReadAgenthailConfig(home)
	if err != nil {
		return turnJudgeConfig{}, false, err
	}
	var fields map[string]json.RawMessage
	promise := config
	err = json.Unmarshal(all[turnJudgeConfigKey], &fields)
	if raw, set := fields["promiseModel"]; err == nil && set {
		var name string
		if string(raw) == "null" || json.Unmarshal(raw, &name) != nil {
			name = ""
		}
		promise.Model = strings.TrimSpace(name)
	}
	if err != nil || promise.Model == "" {
		return turnJudgeConfig{}, false, fmt.Errorf("the Agenthail config %s: %s.promiseModel must name a model", surface.AgenthailConfigPath(home), turnJudgeConfigKey)
	}
	return turnJudgeConfig{Config: config, promise: promise}, true, nil
}

// The facts are read when the turn ends, since the judgment may land later and the work it waits on may have moved on.
func (d *Daemon) queueTurnJudgment(session *surface.Session, turnID, reply string, background *surface.BackgroundTasks) bool {
	j := d.judge
	if j == nil || turnID == "" || strings.TrimSpace(reply) == "" {
		return false
	}
	if _, ok, err := loadTurnJudgeConfig(); err != nil || !ok {
		return false
	}
	facts, err := d.Registry.TurnEndFacts(session.ID)
	if err != nil {
		d.log.Printf("turn judge: read what %s waits on: %s", d.resolveDisplay(session.ID), err)
	}
	if background != nil {
		facts.BackgroundRunning, facts.BackgroundSource, facts.BackgroundTasks = background.Running, background.Source, len(background.Tasks)
	}
	if facts.Typed, err = d.Registry.TurnTyped(session.ID, turnID); err != nil {
		d.log.Printf("turn judge: read who typed the prompt of %s: %s", d.resolveDisplay(session.ID), err)
	}
	judgment := registry.TurnJudgment{SessionID: session.ID, TurnID: turnID, EndedAt: time.Now().UTC(), Facts: facts, Ending: replyEnding(reply, turnJudgeSentences)}
	if end, ok, err := d.Registry.CompletedTurnEnd(session.ID, turnID); err == nil && ok {
		judgment.Headline = end.Headline
	}
	select {
	case j.jobs <- judgeJob{judgment: judgment, reply: lastRunes(reply, turnJudgeReplyRune)}:
		return true
	default:
		j.mu.Lock()
		j.dropped++
		j.mu.Unlock()
		return false
	}
}

func (d *Daemon) startTurnJudge(ctx context.Context) {
	if d.judge == nil {
		return
	}
	for range turnJudgeWorkers {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-d.judge.jobs:
					d.background.run(func(ctx context.Context) { d.judgeTurn(ctx, job) })
				}
			}
		}()
	}
}

func (d *Daemon) judgeTurn(ctx context.Context, job judgeJob) {
	judgment := job.judgment
	config, ok, err := loadTurnJudgeConfig()
	if err != nil || !ok {
		return
	}
	judgment.Model, judgment.PromiseModel = config.Model, config.promise.Model
	callCtx, cancel := context.WithTimeout(ctx, turnJudgeDeadline)
	defer cancel()
	asked := time.Now()
	// The model reads the whole state, and the full reply buries a promise made at the end.
	promise, err := systemone.Client{Config: config.promise, HTTP: d.judge.http}.Ask(callCtx, judgeState{Reply: judgment.Ending}, promiseQuestions)
	var answers map[string]systemone.Answer
	if err == nil {
		answers, err = systemone.Client{Config: config.Config, HTTP: d.judge.http}.Ask(callCtx, judgeState{Reply: judgment.Ending, FullReply: job.reply}, judgeQuestions)
	}
	judgment.JudgedAt, judgment.Ms = time.Now().UTC(), time.Since(asked).Milliseconds()
	if err != nil {
		d.judge.failed(d, err)
		judgment.Error = err.Error()
	} else {
		d.judge.answered(d)
		judgment.Promise, judgment.Needs, judgment.NeedsTail = promise["promise"].Noul, answers["needs"].Noul, answers["needs_tail"].Noul
		judgment.State, judgment.StateP = answers["state"].Choice, answers["state"].Probabilities
	}
	d.recordTurnJudgment(judgment)
}

func (d *Daemon) recordTurnJudgment(judgment registry.TurnJudgment) {
	if err := d.Registry.RecordTurnJudgment(judgment); err != nil {
		d.log.Printf("turn judge: record %s turn %s: %s", d.resolveDisplay(judgment.SessionID), judgment.TurnID, err)
		return
	}
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", judgment.SessionID, map[string]string{"source": "turn-judgment"})
	if !d.pushJudgedTurn(judgment) {
		d.turnVerdictLanded(judgment.SessionID, judgment.TurnID)
	}
}

func (j *turnJudge) failed(d *Daemon, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failures == 0 {
		j.since = time.Now()
		d.log.Printf("turn judge unavailable: %s", err)
	}
	j.failures++
	j.lastError = err.Error()
}

func (j *turnJudge) answered(d *Daemon) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failures > 0 {
		d.log.Printf("turn judge answered again after %d failures since %s", j.failures, j.since.Format(time.RFC3339))
	}
	j.failures, j.lastError, j.since = 0, "", time.Time{}
}

func (d *Daemon) turnJudgeHealth() *turnJudgeHealth {
	if d.judge == nil {
		return nil
	}
	config, ok, err := loadTurnJudgeConfig()
	if err != nil {
		return &turnJudgeHealth{Error: err.Error()}
	}
	if !ok {
		return nil
	}
	d.judge.mu.Lock()
	defer d.judge.mu.Unlock()
	return &turnJudgeHealth{Model: config.Model, PromiseModel: config.promise.Model, Since: d.judge.since, Error: d.judge.lastError, Failures: d.judge.failures, Dropped: d.judge.dropped}
}

func replyEnding(reply string, count int) string {
	sentences := replySentences(strings.Join(replyParagraphs(reply), "\n"))
	return strings.Join(sentences[max(0, len(sentences)-count):], " ")
}

var (
	paragraphBreak = regexp.MustCompile(`\n\s*\n`)
	linkLine       = regexp.MustCompile(`^(?:[-*]\s*)?(?:\[[^\]]*\]\([^)]*\)|<?https?://\S+>?)$`)
	sourcesLine    = regexp.MustCompile(`(?i)^(?:\*\*)?(?:sources|references|links)(?:\*\*)?:?(?:\*\*)?$`)
)

func replyParagraphs(reply string) []string {
	var paragraphs []string
	for _, paragraph := range paragraphBreak.Split(withoutCodeBlocks(reply), -1) {
		if strings.TrimSpace(paragraph) != "" {
			paragraphs = append(paragraphs, paragraph)
		}
	}
	for len(paragraphs) > 1 && linkBlock(paragraphs[len(paragraphs)-1]) {
		paragraphs = paragraphs[:len(paragraphs)-1]
	}
	return paragraphs
}

func linkBlock(paragraph string) bool {
	var lines []string
	for _, line := range strings.Split(paragraph, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > 1 && sourcesLine.MatchString(lines[0]) {
		lines = lines[1:]
	}
	for _, line := range lines {
		if !linkLine.MatchString(line) {
			return false
		}
	}
	return true
}

func replySentences(reply string) []string {
	var out []string
	for _, block := range strings.Split(reply, "\n") {
		block = strings.Join(strings.Fields(block), " ")
		runes := []rune(block)
		start := 0
		for i := 0; i < len(runes); i++ {
			if !strings.ContainsRune(".!?", runes[i]) || i+2 >= len(runes) || !unicode.IsSpace(runes[i+1]) || !sentenceStart(runes[i+2]) {
				continue
			}
			out = appendSentence(out, string(runes[start:i+1]))
			start = i + 2
		}
		out = appendSentence(out, string(runes[start:]))
	}
	return out
}

func sentenceStart(r rune) bool {
	return unicode.IsUpper(r) || strings.ContainsRune("@`*\"(", r)
}

func appendSentence(out []string, sentence string) []string {
	if sentence = strings.TrimSpace(sentence); sentence != "" {
		out = append(out, sentence)
	}
	return out
}

func withoutCodeBlocks(text string) string {
	parts := strings.Split(text, "```")
	var kept []string
	for i, part := range parts {
		if i%2 == 0 {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " ")
}

func lastRunes(text string, n int) string {
	runes := []rune(text)
	return string(runes[max(0, len(runes)-n):])
}
