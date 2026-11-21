package surfaces

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/browsercookie"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAHostSessionSteerCarriesImagesAtPriorityNow(t *testing.T) {
	picture, _ := surfacetest.StoredUploads(t)
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(ctx, uuid.NewString()), sess, "SLOW 30000", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := surface.SteerWithEvidence(ctx, c, sess, "look", surface.Uploads{picture}); err != nil {
		t.Fatal(err)
	}
	if err := c.Interrupt(ctx, sess); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "both turns", 15*time.Second, func() bool { return len(fixture.Turns(t, id)) == 2 })
	if steered := fixture.Turns(t, id)[1]; steered.Priority != "now" || !strings.Contains(steered.Text, "look") {
		t.Fatalf("steer turn=%+v", steered)
	}
}

func TestClaudeHumanTurnCarriesImagesAsContentBlocks(t *testing.T) {
	picture, notes := surfacetest.StoredUploads(t)
	transcript := writeTranscript(t, `
{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"previous"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	var requestBody string
	home, pid := liveHumanTurnProcess(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, home, func(_ context.Context, _, _ string, _ map[string]string, body string, _ browsercookie.Source, _ time.Duration) (int, string, error) {
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

func TestSentImagesRenderAsAttachmentsWithoutRuntimePathNoise(t *testing.T) {
	for _, test := range []struct {
		name, transcript string
		want             []string
	}{
		{
			name: "claude image turn and its saved-image companion",
			transcript: `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + surfacetest.TestPNG + `"}},{"type":"text","text":"what is this?"}]}}
{"type":"user","uuid":"u2","isMeta":true,"turnCompanion":true,"message":{"role":"user","content":[{"type":"text","text":"[Image: source: /tmp/claude/images/1.png]"}]}}`,
			want: []string{"attachment:user:Image attachment", "message:user:what is this?"},
		},
		{
			name: "claude note about a resized image it read",
			transcript: `{"type":"user","uuid":"u4","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-read","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + surfacetest.TestPNG + `"}}]}]}}
{"type":"user","uuid":"u5","isMeta":true,"message":{"role":"user","content":"[Image: original 2016x1456, displayed at 2000x1444. Multiply coordinates by 1.01 to map to original image.]"}}`,
			want: []string{"attachment:user:Image attachment"},
		},
		{
			name:       "claude tool result that is only an image",
			transcript: `{"type":"user","uuid":"u3","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-read","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + surfacetest.TestPNG + `"}}]}]}}`,
			want:       []string{"attachment:user:Image attachment"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(test.transcript+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 20)
			if err != nil {
				t.Fatal(err)
			}
			if got := surfacetest.TimelineSummary(page.Items); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("items=%q want %q", got, test.want)
			}
			for _, item := range page.Items {
				if item.Kind != "attachment" {
					continue
				}
				_, data, err := transcript.ReadAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID, claudeTranscript{}, func(*surface.Session) string { return path })
				if err != nil || len(data) == 0 {
					t.Fatalf("attachment %s is not fetchable: %v", item.Attachment.ID, err)
				}
			}
		})
	}
}

func TestSentFilesRenderAsFilesBesideTheTypedText(t *testing.T) {
	_, notes := surfacetest.StoredUploads(t)
	composed, _ := surface.Uploads{notes}.Compose("read this", true)
	missing := "/daemon/uploads/swept/old.txt"
	for _, test := range []struct {
		source, transcript string
	}{
		{"claude", `{"type":"user","uuid":"u1","message":{"role":"user","content":` + strconv.Quote(composed+"\nAttached file: "+missing) + `}}`},
	} {
		t.Run(test.source, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(test.transcript+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 20)
			if err != nil {
				t.Fatal(err)
			}
			want := []surface.AttachedFile{{Name: "notes.txt", Path: notes.Path, Bytes: 16}, {Name: "old.txt", Path: missing}}
			if len(page.Items) != 1 || page.Items[0].Text != "read this" || !reflect.DeepEqual(page.Items[0].Files, want) {
				t.Fatalf("items=%+v", page.Items)
			}
		})
	}
}

func TestAPersonsImageToAHostSessionArrivesAsAnImageBlock(t *testing.T) {
	picture, notes := surfacetest.StoredUploads(t)
	c, fixture := hostedClaude(t)
	c.localOnly = true
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	message := uuid.NewString()
	ctx := surface.WithMessageUUID(context.Background(), message)
	route := surface.RouteFor(c, sess, surface.AuthorPerson)
	result, err := route.Send(ctx, c, sess, "what is this?", surface.SendOptions{Author: surface.AuthorPerson, Uploads: surface.Uploads{picture, notes}})
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	testkit.Eventually(t, "the turn", 15*time.Second, func() bool { return len(fixture.Turns(t, id)) == 1 })
	data, _ := os.ReadFile(picture.Path)
	want := []any{
		map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(data)}},
		map[string]any{"type": "text", "text": "what is this?\n\nAttached file: " + notes.Path},
	}
	turn := fixture.Turns(t, id)[0]
	var got any
	if err := json.Unmarshal([]byte(turn.Text), &got); err != nil || turn.UUID != message || !reflect.DeepEqual(got, want) {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
}
