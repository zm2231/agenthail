package cli

import (
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestReadsReportEachTurnsMetadataAndNameWhatTheSurfaceDidNotRecord(t *testing.T) {
	duration, reasoning := int64(4200), int64(10)
	recorded := surface.NewTurnMetadata(surface.TurnCompleted, "gpt-fixture", &duration, &surface.TurnUsage{InputTokens: 250, CachedInputTokens: 140, CacheWriteTokens: 5, OutputTokens: 50, ReasoningTokens: &reasoning})
	const wantRecorded = `{"v":1,"state":"completed","model":"gpt-fixture","durationMs":4200,"usage":{"inputTokens":250,"cachedInputTokens":140,"cacheWriteTokens":5,"outputTokens":50,"reasoningTokens":10}}`
	const wantUnrecorded = `{"v":1,"state":"unknown","unavailable":["state","model","durationMs","usage"]}`
	exchanges := []surface.Exchange{
		{User: "first", Assistant: "no metadata"},
		{User: "second", Assistant: reportReply("Done.", `{"v":1,"status":"done","stop":"complete"}`), Turn: recorded},
	}
	for _, read := range readApps() {
		t.Run(read.name, func(t *testing.T) {
			app, target := read.build(t, exchanges)
			output, err := captureStdout(t, func() error { return app.Run([]string{"last", target, "2", "--json"}) })
			var last struct {
				Exchanges []struct {
					Turn json.RawMessage `json:"turn"`
				} `json:"exchanges"`
			}
			if err != nil || json.Unmarshal([]byte(output), &last) != nil || len(last.Exchanges) != 2 || string(last.Exchanges[0].Turn) != wantUnrecorded || string(last.Exchanges[1].Turn) != wantRecorded {
				t.Fatalf("last --json: output=%s err=%v", output, err)
			}

			for _, args := range [][]string{{"reply", target, "--json"}, {"reply", target, "--report"}} {
				app, _ := read.build(t, exchanges)
				output, err := captureStdout(t, func() error { return app.Run(args) })
				var reply struct {
					Turn json.RawMessage `json:"turn"`
				}
				if err != nil || json.Unmarshal([]byte(output), &reply) != nil || string(reply.Turn) != wantRecorded {
					t.Fatalf("%v: output=%s err=%v", args, output, err)
				}
			}

			app, _ = read.build(t, exchanges)
			output, err = captureStdout(t, func() error { return app.Run([]string{"last", target, "2", "--report"}) })
			var reports struct {
				Exchanges []struct {
					Turn json.RawMessage `json:"turn"`
				} `json:"exchanges"`
			}
			if err != nil || json.Unmarshal([]byte(output), &reports) != nil || len(reports.Exchanges) != 2 || string(reports.Exchanges[0].Turn) != wantUnrecorded || string(reports.Exchanges[1].Turn) != wantRecorded {
				t.Fatalf("last --report: output=%s err=%v", output, err)
			}
		})
	}
}
