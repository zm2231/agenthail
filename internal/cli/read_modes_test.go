package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

const fence = "```"

func reportReply(prose, line string) string {
	return prose + "\n\n" + fence + "agenthail-report\n" + line + "\n" + fence
}

type readApp struct {
	name string
	// open builds the app once; set changes the exchanges the session holds.
	open func(t *testing.T) (app *App, target string, set func([]surface.Exchange))
}

func (r readApp) build(t *testing.T, exchanges []surface.Exchange) (*App, string) {
	app, target, set := r.open(t)
	set(exchanges)
	return app, target
}

func readApps() []readApp {
	offline := func(kind surface.SurfaceKind, runtime surface.RuntimeKind) readApp {
		return readApp{name: string(kind), open: func(t *testing.T) (*App, string, func([]surface.Exchange)) {
			session := surface.Session{ID: "s", Surface: kind, RuntimeKind: runtime, Status: surface.StatusIdle}
			fake := &cliSurface{kind: kind, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}}
			app, _ := cliFixture(t, fake)
			return app, string(kind) + ":s", func(exchanges []surface.Exchange) { fake.tail = exchanges }
		}}
	}
	journal := func(app *App) func([]surface.Exchange) {
		var held []surface.Exchange
		app.daemonSessionPageReader = func() (sessionPageReader, error) {
			return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: held}}, nil
		}
		return func(exchanges []surface.Exchange) { held = exchanges }
	}
	return []readApp{
		offline(surface.KindCodex, surface.RuntimeCodexDesktop),
		offline(surface.KindClaude, surface.RuntimeClaudeCLI),
		offline(surface.KindNotion, surface.RuntimeNotionWeb),
		{name: "paired host", open: func(t *testing.T) (*App, string, func([]surface.Exchange)) {
			app, _, _, _ := federatedCLIFixture(t, true)
			return app, "vps::@builder", journal(app)
		}},
		{name: "daemon page", open: func(t *testing.T) (*App, string, func([]surface.Exchange)) {
			session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
			app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}})
			app.catalogDaemonRunning = func() bool { return true }
			return app, "codex:s", journal(app)
		}},
	}
}

func TestMaxBytesCutsRepliesOnACharacterBoundaryAndSaysWhatWasCut(t *testing.T) {
	text := "Résumé ready 🙂 done"
	emoji := strings.Index(text, "🙂")
	for _, read := range readApps() {
		t.Run(read.name, func(t *testing.T) {
			app, target, set := read.open(t)
			for _, limit := range []struct {
				bytes int
				want  string
			}{
				{len(text), text},
				{len(text) + 1, text},
				{emoji + 2, text[:emoji]},
				{emoji + 4, text[:emoji+4]},
				{1, "R"},
			} {
				set([]surface.Exchange{{User: "prompt " + text, Assistant: text, Timestamp: time.Unix(1_700_000_000, 0)}})
				cut := limit.want != text
				output, err := captureStdout(t, func() error {
					return app.Run([]string{"reply", target, "--json", "--max-bytes", itoa(limit.bytes)})
				})
				var reply struct {
					Text      string `json:"text"`
					Truncated bool   `json:"truncated"`
					TextBytes int    `json:"textBytes"`
				}
				if err != nil || json.Unmarshal([]byte(output), &reply) != nil || reply.Text != limit.want || reply.Truncated != cut || (cut && reply.TextBytes != len(text)) || (!cut && reply.TextBytes != 0) {
					t.Fatalf("reply --max-bytes %d: output=%s err=%v", limit.bytes, output, err)
				}

				output, err = captureStdout(t, func() error {
					return app.Run([]string{"last", target, "--json", "--max-bytes", itoa(limit.bytes)})
				})
				var last struct {
					Exchanges []struct {
						User               string `json:"user"`
						UserTruncated      bool   `json:"userTruncated"`
						UserBytes          int    `json:"userBytes"`
						Assistant          string `json:"assistant"`
						AssistantTruncated bool   `json:"assistantTruncated"`
						AssistantBytes     int    `json:"assistantBytes"`
					} `json:"exchanges"`
				}
				if err != nil || json.Unmarshal([]byte(output), &last) != nil || len(last.Exchanges) != 1 {
					t.Fatalf("last --max-bytes %d: output=%s err=%v", limit.bytes, output, err)
				}
				got := last.Exchanges[0]
				if got.Assistant != limit.want || got.AssistantTruncated != cut || (cut && got.AssistantBytes != len(text)) || !got.UserTruncated || got.UserBytes != len("prompt "+text) || len(got.User) > limit.bytes || !utf8.ValidString(got.User) {
					t.Fatalf("last --max-bytes %d: %+v", limit.bytes, got)
				}

				output, err = captureStdout(t, func() error {
					return app.Run([]string{"reply", target, "--max-bytes", itoa(limit.bytes)})
				})
				note := "[truncated: " + itoa(len(limit.want)) + " of " + itoa(len(text)) + " bytes]"
				if err != nil || !strings.Contains(output, limit.want) || strings.Contains(output, note) != cut {
					t.Fatalf("reply text --max-bytes %d: output=%q err=%v", limit.bytes, output, err)
				}
			}
		})
	}
}

func TestMaxBytesInsideTheFirstCharacterStillSaysTheFieldWasCut(t *testing.T) {
	for _, read := range readApps() {
		t.Run(read.name, func(t *testing.T) {
			app, target := read.build(t, []surface.Exchange{{User: "🙂 prompt", Assistant: "🙂 a longer answer"}})
			for limit := 1; limit < len("🙂"); limit++ {
				output, err := captureStdout(t, func() error { return app.Run([]string{"last", target, "--max-bytes", itoa(limit)}) })
				note := "[truncated: 0 of " + itoa(len("🙂 prompt")) + " bytes]"
				if err != nil || !utf8.ValidString(output) || strings.Count(output, note) != 1 || !strings.Contains(output, "[truncated: 0 of "+itoa(len("🙂 a longer answer"))+" bytes]") {
					t.Fatalf("--max-bytes %d: output=%q err=%v", limit, output, err)
				}
			}
		})
	}
}

func TestMaxBytesOnAnEmptySessionPrintsNoExchanges(t *testing.T) {
	for _, read := range readApps() {
		t.Run(read.name, func(t *testing.T) {
			app, target := read.build(t, nil)
			output, err := captureStdout(t, func() error { return app.Run([]string{"last", target, "--json", "--max-bytes", "10"}) })
			if err != nil || !strings.Contains(output, `"exchanges":[]`) {
				t.Fatalf("output=%s err=%v", output, err)
			}
		})
	}
}

func TestReportReadsOnlyTheCompletionReportAtTheEndOfTheReply(t *testing.T) {
	valid := `{"v":1,"status":"blocked","stop":"needsInput","reason":"Which retry limit?","commits":["0d825e4d"],"next":"Choose a limit"}`
	for _, read := range readApps() {
		t.Run(read.name, func(t *testing.T) {
			app, target, set := read.open(t)
			for _, test := range []struct {
				name, reply, want string
			}{
				{"valid", reportReply("Paused for a decision.", valid), ""},
				{"absent", "All done, no report.", "no completion report at the end of the reply"},
				{"quoted mid-reply", reportReply("Example:", valid) + "\n\nThat was an example.", "no completion report at the end of the reply"},
				{"unclosed", "Done.\n\n" + fence + "agenthail-report\n" + valid + "\nand then more text", "no completion report at the end of the reply"},
				{"another fence", "Done.\n\n" + fence + "json\n" + valid + "\n" + fence, "no completion report at the end of the reply"},
				{"whole reply", fence + "agenthail-report\n" + valid + "\n" + fence + "  \n\n", ""},
				{"crlf", strings.ReplaceAll(reportReply("Paused for a decision.", valid), "\n", "\r\n"), "no completion report at the end of the reply"},
				{"space after the info string", "Done.\n\n" + fence + "agenthail-report \n" + valid + "\n" + fence, "no completion report at the end of the reply"},
				{"empty block", "Done.\n\n" + fence + "agenthail-report\n" + fence, "malformed completion report: the block holds 0 lines"},
				{"body opens a fence", "Done.\n\n" + fence + "agenthail-report\n" + fence + "json\n" + fence, "no completion report at the end of the reply"},
				{"two lines", reportReply("Done.", `{"v":1,`+"\n"+`"status":"done","stop":"complete"}`), "malformed completion report: the block holds 2 lines"},
				{"newer version", reportReply("Done.", `{"v":2,"status":"done","stop":"complete"}`), "malformed completion report: version 2 is not supported"},
				{"status and stop disagree", reportReply("Done.", `{"v":1,"status":"done","stop":"needsInput"}`), `stop "needsInput" does not go with status "done"`},
				{"blocked without reason", reportReply("Stuck.", `{"v":1,"status":"blocked","stop":"needsInput"}`), `status "blocked" needs a reason`},
				{"not JSON", reportReply("Done.", `status: done`), "malformed completion report: not a JSON object"},
				{"reason at the limit", reportReply("Stuck.", `{"v":1,"status":"blocked","stop":"needsInput","reason":"`+strings.Repeat("é", 280)+`"}`), ""},
				{"reason over the limit", reportReply("Stuck.", `{"v":1,"status":"blocked","stop":"needsInput","reason":"`+strings.Repeat("é", 281)+`"}`), "reason is 281 characters; at most 280"},
				{"line over the limit", reportReply("Done.", `{"v":1,"status":"done","stop":"complete","next":"`+strings.Repeat("x", 2048)+`"}`), "the JSON line is 2099 bytes; at most 2048"},
				{"too many commits", reportReply("Done.", `{"v":1,"status":"done","stop":"complete","commits":["a"`+strings.Repeat(`,"a"`, 20)+`]}`), "commits lists 21 entries; at most 20"},
				{"empty path", reportReply("Done.", `{"v":1,"status":"done","stop":"complete","paths":[""]}`), "paths has an empty entry"},
			} {
				set([]surface.Exchange{{User: "work", Assistant: test.reply}})
				output, err := captureStdout(t, func() error { return app.Run([]string{"reply", target, "--report"}) })
				if test.want != "" {
					if err == nil || !strings.Contains(err.Error(), test.want) || output != "" {
						t.Fatalf("%s: output=%q err=%v", test.name, output, err)
					}
					continue
				}
				var answer struct {
					Report map[string]any `json:"report"`
				}
				if test.name == "reason at the limit" {
					if err != nil || json.Unmarshal([]byte(output), &answer) != nil || answer.Report["reason"] != strings.Repeat("é", 280) {
						t.Fatalf("%s: output=%s err=%v", test.name, output, err)
					}
					continue
				}
				if err != nil || json.Unmarshal([]byte(output), &answer) != nil || answer.Report["status"] != "blocked" || answer.Report["reason"] != "Which retry limit?" || strings.Contains(output, "Paused for a decision.") {
					t.Fatalf("%s: output=%s err=%v", test.name, output, err)
				}
			}
		})
	}
}

func TestLastReportFailsOnlyWhenTheNewestExchangeHasNoReport(t *testing.T) {
	done := reportReply("Shipped.", `{"v":1,"status":"done","stop":"complete"}`)
	for _, read := range readApps() {
		t.Run(read.name, func(t *testing.T) {
			app, target, set := read.open(t)
			set([]surface.Exchange{{User: "status?", Assistant: "Still working."}, {User: "work", Assistant: done}})
			output, err := captureStdout(t, func() error { return app.Run([]string{"last", target, "2", "--report"}) })
			var document struct {
				Exchanges []struct {
					Report      map[string]any `json:"report"`
					ReportError string         `json:"reportError"`
					Assistant   *string        `json:"assistant"`
				} `json:"exchanges"`
			}
			if err != nil || json.Unmarshal([]byte(output), &document) != nil || len(document.Exchanges) != 2 {
				t.Fatalf("output=%s err=%v", output, err)
			}
			if older, newest := document.Exchanges[0], document.Exchanges[1]; older.ReportError != "no completion report at the end of the reply" || older.Report != nil || newest.Report["status"] != "done" || newest.ReportError != "" || newest.Assistant != nil {
				t.Fatalf("exchanges=%+v", document.Exchanges)
			}

			set([]surface.Exchange{{User: "work", Assistant: done}, {User: "status?", Assistant: "Still working."}})
			output, err = captureStdout(t, func() error { return app.Run([]string{"last", target, "2", "--report"}) })
			if err == nil || !strings.Contains(err.Error(), "newest exchange: no completion report") || !strings.Contains(output, `"status":"done"`) {
				t.Fatalf("newest without a report: output=%s err=%v", output, err)
			}

			set(nil)
			output, err = captureStdout(t, func() error { return app.Run([]string{"last", target, "--report"}) })
			if err == nil || !strings.Contains(err.Error(), "the session has no exchanges") || !strings.Contains(output, `"exchanges":[]`) {
				t.Fatalf("empty: output=%s err=%v", output, err)
			}
		})
	}
}

func TestReadModesRefuseAmbiguousFlags(t *testing.T) {
	read := readApps()[0]
	for _, args := range [][]string{
		{"--report", "--max-bytes", "100"},
		{"--max-bytes", "0"},
		{"--max-bytes", "lots"},
		{"--max-bytes"},
	} {
		for _, command := range []string{"reply", "last"} {
			app, target := read.build(t, []surface.Exchange{{User: "q", Assistant: "a"}})
			output, err := captureStdout(t, func() error { return app.Run(append([]string{command, target}, args...)) })
			if err == nil || output != "" {
				t.Fatalf("%s %v: output=%q err=%v", command, args, output, err)
			}
		}
	}
}

func itoa(n int) string {
	data, _ := json.Marshal(n)
	return string(data)
}
