package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeModelControlCorrelatesExactRequest(t *testing.T) {
	for _, outcome := range []string{"success", "error", "timeout", "http400"} {
		t.Run(outcome, func(t *testing.T) {
			var requestID, postedUUID string
			posts := 0
			client := NewClaudeWithRequest("Default", t.TempDir(), func(ctx context.Context, method, url string, headers map[string]string, body, bridge, origin string, timeout time.Duration) (int, string, error) {
				if method == "POST" {
					posts++
					var payload struct {
						Events []struct {
							Payload struct {
								Type      string `json:"type"`
								RequestID string `json:"request_id"`
								UUID      string `json:"uuid"`
							} `json:"payload"`
						} `json:"events"`
					}
					if err := json.Unmarshal([]byte(body), &payload); err != nil {
						t.Fatal(err)
					}
					requestID = payload.Events[0].Payload.RequestID
					postedUUID = payload.Events[0].Payload.UUID
					if payload.Events[0].Payload.Type != "control_request" {
						t.Fatal("model became a text message")
					}
					if outcome == "http400" {
						return 400, `{}`, nil
					}
					return 200, `{}`, nil
				}
				if strings.Contains(url, "/events?") {
					if !strings.Contains(url, "after_id="+postedUUID) {
						t.Fatalf("missing request cursor: %s", url)
					}
					if outcome == "timeout" {
						return 200, `{"data":[]}`, nil
					}
					return 200, fmt.Sprintf(`{"data":[{"type":"control_response","response":{"request_id":"other-request","subtype":"success"}},{"type":"control_response","response":{"request_id":%q,"subtype":%q,"error":"unavailable model"}}],"last_id":"response"}`, requestID, outcome), nil
				}
				return 200, `{"response_shape":{"id":"cse_test","connection_status":"connected","external_metadata":{"model":"current-runtime-model"}}}`, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
			defer cancel()
			result, err := client.Control(ctx, &surface.Session{ID: "session_test"}, surface.ControlRequest{Action: "model", Value: "haiku"})
			if result == nil || result.RequestID != requestID || posts != 1 {
				t.Fatalf("result=%+v posts=%d err=%v", result, posts, err)
			}
			switch outcome {
			case "success":
				if err != nil || result.Evidence != surface.EvidenceDelivered {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			case "error", "http400":
				if err == nil || result.Evidence != surface.EvidenceFailed {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			case "timeout":
				if !surface.IsDeliveryOutcomeUnknown(err) || result.Evidence != surface.EvidenceUnknown {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			}
		})
	}
}

func TestClaudeCommandRequiresLinkedLocalExecution(t *testing.T) {
	for _, rewritten := range []bool{false, true} {
		t.Run(fmt.Sprint(rewritten), func(t *testing.T) {
			path := writeTranscript(t, `{"type":"user","uuid":"previous","message":{"content":"old"}}`)
			var uuid string
			client := NewClaudeWithRequest("Default", t.TempDir(), func(ctx context.Context, method, url string, headers map[string]string, body, bridge, origin string, timeout time.Duration) (int, string, error) {
				if method == "POST" {
					if headers["anthropic-client-platform"] != "web_claude_ai" || headers["anthropic-client-feature"] != "ccr" {
						t.Fatal("browser command headers missing")
					}
					var payload struct {
						Events []struct {
							Payload struct {
								UUID    string `json:"uuid"`
								Message struct {
									Content string `json:"content"`
								} `json:"message"`
							} `json:"payload"`
						} `json:"events"`
					}
					if err := json.Unmarshal([]byte(body), &payload); err != nil {
						t.Fatal(err)
					}
					uuid = payload.Events[0].Payload.UUID
					if payload.Events[0].Payload.Message.Content != "/context" {
						t.Fatal(body)
					}
					parent := uuid
					if rewritten {
						parent = "local-echo"
					}
					file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					_, err = fmt.Fprintf(file, "\n{\"type\":\"system\",\"subtype\":\"local_command\",\"uuid\":\"unrelated\",\"parentUuid\":\"other\",\"commandRun\":{\"command\":\"context\",\"args\":\"\"},\"content\":\"wrong\"}\n{\"type\":\"system\",\"subtype\":\"local_command\",\"uuid\":\"output\",\"parentUuid\":%q,\"commandRun\":{\"command\":\"context\",\"args\":\"\"},\"content\":\"<local-command-stdout>actual context</local-command-stdout>\"}\n", parent)
					file.Close()
					if err != nil {
						t.Fatal(err)
					}
					return 200, `{}`, nil
				}
				if strings.Contains(url, "/events?") {
					return 200, `{"data":[{"type":"assistant","uuid":"output"}],"last_id":"output"}`, nil
				}
				return 200, `{"response_shape":{"id":"cse_test","connection_status":"connected"}}`, nil
			})
			result, err := client.Control(context.Background(), &surface.Session{ID: "session_test", Transcript: path}, surface.ControlRequest{Action: "context"})
			if err != nil || result.RequestID != uuid || result.Output != "actual context" || result.Evidence != surface.EvidenceReplyObserved {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestClaudeInterruptNeedsOriginalTerminalTurn(t *testing.T) {
	for _, ended := range []bool{false, true} {
		t.Run(fmt.Sprint(ended), func(t *testing.T) {
			path := writeTranscript(t, `{"type":"user","uuid":"active","message":{"content":"working"}}`)
			var requestID string
			client := NewClaudeWithRequest("Default", t.TempDir(), func(ctx context.Context, method, url string, headers map[string]string, body, bridge, origin string, timeout time.Duration) (int, string, error) {
				if method == "POST" {
					var payload struct {
						Events []struct {
							Payload struct {
								RequestID string `json:"request_id"`
							} `json:"payload"`
						} `json:"events"`
					}
					json.Unmarshal([]byte(body), &payload)
					requestID = payload.Events[0].Payload.RequestID
					if ended {
						file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
						if err != nil {
							t.Fatal(err)
						}
						fmt.Fprintln(file, `{"type":"user","uuid":"interrupt-marker","message":{"content":"[Request interrupted by user]"}}`)
						file.Close()
					}
					return 200, `{}`, nil
				}
				if strings.Contains(url, "/events?") {
					return 200, fmt.Sprintf(`{"data":[{"type":"control_response","response":{"request_id":%q,"subtype":"success"}}],"last_id":"response"}`, requestID), nil
				}
				return 200, `{"response_shape":{"id":"cse_test","connection_status":"connected"}}`, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
			defer cancel()
			result, err := client.Control(ctx, &surface.Session{ID: "session_test", Transcript: path}, surface.ControlRequest{Action: "interrupt"})
			if result == nil || result.TurnID != "active" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if ended && err != nil {
				t.Fatal(err)
			}
			if !ended && (!surface.IsDeliveryOutcomeUnknown(err) || result.Evidence != surface.EvidenceUnknown) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestClaudeModelReadsRuntimeNotPreviousAssistant(t *testing.T) {
	client := NewClaudeWithRequest("Default", t.TempDir(), func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
		return 200, `{"response_shape":{"id":"cse_test","connection_status":"connected","external_metadata":{"model":"runtime-model"}}}`, nil
	})
	model, err := client.Model(context.Background(), &surface.Session{ID: "session_test", Transcript: writeTranscript(t, `{"type":"assistant","message":{"model":"stale-model"}}`)}, "")
	if err != nil || model != "runtime-model" {
		t.Fatalf("model=%s err=%v", model, err)
	}
}

func TestClaudeControlDoesNotPostToDisconnectedTarget(t *testing.T) {
	posts := 0
	client := NewClaudeWithRequest("Default", t.TempDir(), func(ctx context.Context, method, url string, headers map[string]string, body, bridge, origin string, timeout time.Duration) (int, string, error) {
		if method == "POST" {
			posts++
		}
		return 200, `{"response_shape":{"id":"cse_test","connection_status":"disconnected"}}`, nil
	})
	_, err := client.Control(context.Background(), &surface.Session{ID: "session_test"}, surface.ControlRequest{Action: "model", Value: "haiku"})
	if err == nil || posts != 0 {
		t.Fatalf("posts=%d err=%v", posts, err)
	}
}

func TestClaudeCommandsDoNotCreatePhantomActiveTurns(t *testing.T) {
	for _, command := range []string{"context", "usage", "effort medium", "rename worker"} {
		t.Run(command, func(t *testing.T) {
			path := writeTranscript(t, fmt.Sprintf(`{"type":"user","uuid":"old","message":{"content":"work"}}
{"type":"assistant","uuid":"answer","message":{"id":"done","stop_reason":"end_turn","content":[{"type":"text","text":"finished"}]}}
{"type":"user","uuid":"control","message":{"content":%q}}
{"type":"system","subtype":"local_command","parentUuid":"control","content":"<local-command-stdout>result</local-command-stdout>"}`, "/"+command))
			client := NewClaude("Default", t.TempDir())
			observation, err := client.Observe(context.Background(), &surface.Session{ID: "session_test", Transcript: path})
			if err != nil || observation.Status != surface.StatusIdle || observation.ActiveTurnID != "" {
				t.Fatalf("observation=%+v err=%v", observation, err)
			}
		})
	}
	if !isHumanTranscriptText("/contextual-reconstruction investigate this") {
		t.Fatal("skill invocation was hidden")
	}
}
