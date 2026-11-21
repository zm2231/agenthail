package codexsurface

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func TestCodexSteerTakesImagesAsLocalImageInput(t *testing.T) {
	picture, _ := surfacetest.StoredUploads(t)
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail"})
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}
	if _, err := surface.RouteSend.Send(context.Background(), codex, session, "start", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	result, err := surface.SteerWithEvidence(context.Background(), codex, session, "look", surface.Uploads{picture})
	if err != nil || result.Evidence != surface.EvidenceDelivered {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	steers := fake.Calls("turn/steer")
	if len(steers) != 1 {
		t.Fatalf("turn/steer calls=%v", fake.Methods())
	}
	var input []map[string]any
	encoded, _ := json.Marshal(steers[0]["input"])
	_ = json.Unmarshal(encoded, &input)
	want := []map[string]any{{"type": "text", "text": "look"}, {"type": "localImage", "path": picture.Path}}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("input=%v want %v", input, want)
	}
}

func TestCodexTurnTakesImagesAsLocalImageInputAndFilesAsPaths(t *testing.T) {
	picture, notes := surfacetest.StoredUploads(t)
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

func TestCodexSentImagesRenderAsAttachmentsWithoutRuntimePathNoise(t *testing.T) {
	for _, test := range []struct {
		name, transcript string
		want             []string
	}{
		{
			name:       "codex local image input",
			transcript: `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"what is this?"},{"type":"input_text","text":"<image name=[Image #1] path=\"/daemon/uploads/shot.png\">"},{"type":"input_image","image_url":"data:image/png;base64,` + surfacetest.TestPNG + `","detail":"high"},{"type":"input_text","text":"</image>"}]}}`,
			want:       []string{"message:user:what is this?", "attachment:user:Image attachment"},
		},
		{
			name: "codex item records that mirror the sent turn",
			transcript: `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"what is this?"},{"type":"input_image","image_url":"data:image/png;base64,` + surfacetest.TestPNG + `"}]}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"what is this?"},{"type":"local_image","path":"/daemon/uploads/shot.png"}]}}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A red square."}]}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"A red square."}]}}}`,
			want: []string{"message:user:what is this?", "attachment:user:Image attachment", "message:assistant:A red square."},
		},
		{
			name:       "codex image without text",
			transcript: `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<image name=[Image #1]>"},{"type":"input_image","image_url":"data:image/png;base64,` + surfacetest.TestPNG + `"},{"type":"input_text","text":"</image>"}]}}`,
			want:       []string{"attachment:user:Image attachment"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(test.transcript+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 20)
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
				_, data, err := transcript.ReadAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID, codexTranscript{}, func(*surface.Session) string { return path })
				if err != nil || len(data) == 0 {
					t.Fatalf("attachment %s is not fetchable: %v", item.Attachment.ID, err)
				}
			}
		})
	}
}

func TestCodexSentFilesRenderAsFilesBesideTheTypedText(t *testing.T) {
	_, notes := surfacetest.StoredUploads(t)
	composed, _ := surface.Uploads{notes}.Compose("read this", true)
	missing := "/daemon/uploads/swept/old.txt"
	for _, test := range []struct {
		source, transcript string
	}{
		{"codex", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":` + strconv.Quote(composed+"\nAttached file: "+missing) + `}]}}`},
	} {
		t.Run(test.source, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(test.transcript+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 20)
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
