package codexsurface

import (
	"context"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

// TurnContext is what Agenthail tells a thread with a turn it starts or
// steers, the key Codex holds it under, and delivered, called once Codex has
// taken the input. compactedSince is nil when the app-server puts that
// context back after a compaction; otherwise it reports whether the thread
// compacted since a time. Empty text adds nothing.
type TurnContext func(ctx context.Context, session surface.Session, compactedSince func(time.Time) (bool, error)) (key, text string, delivered func())

// Codex 0.171.0 puts the additional context it holds back after compacting
// a thread; 0.163.0 to 0.170.0 drop it.
var codexKeepsContextThroughCompaction = [3]int{0, 171, 0}

type turnContextAttachment struct {
	mu      sync.Mutex
	context TurnContext
}

func (a *turnContextAttachment) SetTurnContext(context TurnContext) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.context = context
}

// withTurnContext adds the context as a developer message Codex records in
// the turn. Codex takes additionalContext only from a connection that
// negotiated experimentalApi, and refuses the whole turn otherwise.
func (a *turnContextAttachment) withTurnContext(ctx context.Context, client codexClient, session surface.Session, params map[string]any) func() {
	a.mu.Lock()
	turnContext := a.context
	a.mu.Unlock()
	managed, experimental := client.(*managedCodexClient)
	if !experimental || turnContext == nil {
		return func() {}
	}
	var compactedSince func(time.Time) (bool, error)
	if managed.dropsContextOnCompaction() {
		compactedSince = func(since time.Time) (bool, error) {
			return codexCompactedSince(ctx, client, session.ID, since)
		}
	}
	key, text, delivered := turnContext(ctx, session, compactedSince)
	if text != "" {
		params["additionalContext"] = map[string]any{key: map[string]any{"value": text, "kind": "application"}}
	}
	if delivered == nil {
		return func() {}
	}
	return delivered
}

// An app-server whose version is unknown is taken to be a newer one.
func (c *managedCodexClient) dropsContextOnCompaction() bool {
	return c.versionKnown && versionBefore(c.version, codexKeepsContextThroughCompaction)
}

var codexVersionPattern = regexp.MustCompile(`/(\d+)\.(\d+)\.(\d+)`)

func codexVersionOf(userAgent string) ([3]int, bool) {
	match := codexVersionPattern.FindStringSubmatch(userAgent)
	if match == nil {
		return [3]int{}, false
	}
	var version [3]int
	for index := range version {
		version[index], _ = strconv.Atoi(match[index+1])
	}
	return version, true
}

func versionBefore(version, other [3]int) bool {
	for index := range version {
		if version[index] != other[index] {
			return version[index] < other[index]
		}
	}
	return false
}

const compactionPage = 10

// compactedAfter reads when a compaction started from its item, or, from an
// older app-server that does not time items, from its turn to
// the second, so a compaction within the second before since counts too.
func compactedAfter(item, turn map[string]any, since time.Time) bool {
	if started, ok := item["startedAtMs"]; ok {
		return codexNumber(started) >= since.UnixMilli()
	}
	return codexNumber(turn["startedAt"]) >= since.Unix()
}

// codexCompactedSince reports whether the thread has a contextCompaction
// item that started at or after since, reading its turns newest first and
// stopping at the first turn that ended before since.
func codexCompactedSince(ctx context.Context, client codexClient, threadID string, since time.Time) (bool, error) {
	cursor := ""
	for {
		params := map[string]any{"threadId": threadID, "limit": compactionPage, "sortDirection": "desc", "itemsView": "full"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		response, err := client.Request(ctx, "thread/turns/list", params, 10*time.Second)
		if err != nil {
			return false, err
		}
		result, _ := response["result"].(map[string]any)
		turns, _ := result["data"].([]any)
		for _, value := range turns {
			turn, _ := value.(map[string]any)
			items, _ := turn["items"].([]any)
			for _, value := range items {
				item, _ := value.(map[string]any)
				if transcript.String(item, "type") == "contextCompaction" && compactedAfter(item, turn, since) {
					return true, nil
				}
			}
			if ended := codexNumber(turn["completedAt"]); ended > 0 && time.Unix(ended, 0).Add(time.Second).Before(since) {
				return false, nil
			}
		}
		cursor = transcript.String(result, "nextCursor")
		if cursor == "" || len(turns) == 0 {
			return false, nil
		}
	}
}
