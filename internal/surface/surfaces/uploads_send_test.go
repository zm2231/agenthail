package surfaces

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

func storedUploads(t *testing.T) (surface.Upload, surface.Upload) {
	t.Helper()
	dir := t.TempDir()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	imagePath, notesPath := filepath.Join(dir, "shot.png"), filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(imagePath, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notesPath, []byte("synthetic notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return surface.Upload{ID: "upload:image", Name: "shot.png", Path: imagePath, MediaType: "image/png"},
		surface.Upload{ID: "upload:notes", Name: "notes.txt", Path: notesPath, MediaType: "text/plain; charset=utf-8"}
}

func TestCodexTurnTakesImagesAsLocalImageInputAndFilesAsPaths(t *testing.T) {
	picture, notes := storedUploads(t)
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail"})
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: codexTransportManaged}
	result, err := surface.RouteSend.Send(context.Background(), codex, session, "compare", surface.SendOptions{Uploads: surface.Uploads{picture, notes}})
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	starts := fake.Calls("turn/start")
	if len(starts) != 1 {
		t.Fatalf("turn/start calls=%v", fake.Methods())
	}
	var input []map[string]any
	encoded, _ := json.Marshal(starts[0]["input"])
	_ = json.Unmarshal(encoded, &input)
	want := []map[string]any{
		{"type": "text", "text": "compare\n\nAttached file: " + notes.Path},
		{"type": "localImage", "path": picture.Path},
	}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input=%v want %v", input, want)
	}
}

func TestClaudeHumanTurnCarriesImagesAsContentBlocks(t *testing.T) {
	picture, notes := storedUploads(t)
	transcript := writeTranscript(t, `
{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"previous"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	var requestBody string
	home, pid := liveHumanTurnProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(_ context.Context, _, _ string, _ map[string]string, body string, _ CookieSource, _ time.Duration) (int, string, error) {
		requestBody = body
		return http.StatusOK, `{}`, nil
	})
	session := &surface.Session{ID: "conversation-bridge", Bridge: "session_bridge", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Transport: "uds", Status: surface.StatusIdle, Transcript: transcript}
	route := claude.Route(session, surface.AuthorPerson)
	result, err := route.Send(context.Background(), claude, session, "what changed?", surface.SendOptions{Author: surface.AuthorPerson, Uploads: surface.Uploads{picture, notes}})
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("route=%v result=%+v err=%v", route, result, err)
	}
	var envelope struct {
		Events []struct {
			Payload struct {
				Message struct {
					Content []map[string]any `json:"content"`
				} `json:"message"`
			} `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(requestBody), &envelope); err != nil || len(envelope.Events) != 1 {
		t.Fatalf("body=%s err=%v", requestBody, err)
	}
	data, _ := os.ReadFile(picture.Path)
	want := []map[string]any{
		{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(data)}},
		{"type": "text", "text": "what changed?\n\nAttached file: " + notes.Path},
	}
	if got := envelope.Events[0].Payload.Message.Content; !reflect.DeepEqual(got, want) {
		t.Fatalf("content=%v", got)
	}
}

func timelineSummary(items []surface.TimelineItem) []string {
	summary := make([]string, 0, len(items))
	for _, item := range items {
		entry := item.Kind + ":" + item.Role + ":" + item.Text
		if item.Kind == "attachment" && item.Attachment == nil {
			entry += ":unresolved"
		}
		summary = append(summary, entry)
	}
	return summary
}

func TestSentImagesRenderAsAttachmentsWithoutRuntimePathNoise(t *testing.T) {
	for _, test := range []struct {
		name, source, transcript string
		want                     []string
	}{
		{
			name:       "codex local image input",
			source:     "codex",
			transcript: `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"what is this?"},{"type":"input_text","text":"<image name=[Image #1] path=\"/daemon/uploads/shot.png\">"},{"type":"input_image","image_url":"data:image/png;base64,` + testPNG + `","detail":"high"},{"type":"input_text","text":"</image>"}]}}`,
			want:       []string{"message:user:what is this?", "attachment:user:Image attachment"},
		},
		{
			name:       "codex image without text",
			source:     "codex",
			transcript: `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<image name=[Image #1]>"},{"type":"input_image","image_url":"data:image/png;base64,` + testPNG + `"},{"type":"input_text","text":"</image>"}]}}`,
			want:       []string{"attachment:user:Image attachment"},
		},
		{
			name:   "claude image turn and its saved-image companion",
			source: "claude",
			transcript: `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}},{"type":"text","text":"what is this?"}]}}
{"type":"user","uuid":"u2","isMeta":true,"turnCompanion":true,"message":{"role":"user","content":[{"type":"text","text":"[Image: source: /tmp/claude/images/1.png]"}]}}`,
			want: []string{"attachment:user:Image attachment", "message:user:what is this?"},
		},
		{
			name:       "claude tool result that is only an image",
			source:     "claude",
			transcript: `{"type":"user","uuid":"u3","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-read","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}]}}`,
			want:       []string{"attachment:user:Image attachment"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(test.transcript+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			page, err := readTimeline(context.Background(), path, test.source, 0, 20)
			if err != nil {
				t.Fatal(err)
			}
			if got := timelineSummary(page.Items); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("items=%q want %q", got, test.want)
			}
			for _, item := range page.Items {
				if item.Kind != "attachment" {
					continue
				}
				_, data, err := readTranscriptAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID, test.source, func(*surface.Session) string { return path })
				if err != nil || len(data) == 0 {
					t.Fatalf("attachment %s is not fetchable: %v", item.Attachment.ID, err)
				}
			}
		})
	}
}
