package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/hosts"
)

func followingHostSend(t *testing.T, result string) (*App, *followedStreams) {
	t.Helper()
	app, _, _, api := federatedCLIFixture(t, true)
	api.response = `{"ok":true,"result":` + result + `}`
	streams := &followedStreams{replies: hosts.Qualify(vpsHostID, "remote-2")}
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return testSessionPageReader{}, nil }
	app.daemonSessionStreamReader = func() (sessionStreamReader, error) { return streams, nil }
	return app, streams
}

func TestAHostSendThatStartsANewLeadFollowsItFromItsStart(t *testing.T) {
	started := hosts.Qualify(vpsHostID, "remote-2")
	for _, flags := range [][]string{{"--reply"}, {"--reply", "--json"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			app, streams := followingHostSend(t, `{"evidence":"delivered","status":"sent","sessionId":"`+started+`","turnId":"started-turn","started":true,"detail":"Started a new conversation for coordinator @builder."}`)
			var output string
			stderr, err := captureStderr(t, func() error {
				var runErr error
				output, runErr = captureStdout(t, func() error {
					return app.Run(append([]string{"send", "vps::@builder", "task", "--timeout", "3s"}, flags...))
				})
				return runErr
			})
			if err != nil || !strings.Contains(output, "the new lead's reply") || strings.Contains(stderr, "cannot follow") {
				t.Fatalf("output=%q stderr=%q err=%v, want the new conversation's reply", output, stderr, err)
			}
			if len(flags) == 2 {
				var answer struct {
					Delivery struct {
						SessionID string `json:"sessionId"`
						Started   bool   `json:"started"`
					} `json:"delivery"`
					Reply struct {
						Text string `json:"text"`
					} `json:"reply"`
				}
				if err := json.Unmarshal([]byte(output), &answer); err != nil || answer.Delivery.SessionID != started || !answer.Delivery.Started || answer.Reply.Text != "the new lead's reply" {
					t.Fatalf("json=%q err=%v, want the delivery to the new conversation and its reply", output, err)
				}
			} else if !strings.Contains(output, "Started a new conversation for coordinator @builder") {
				t.Fatalf("output=%q, want word that it started", output)
			}
			want := []string{"open " + vpsBuilder.ID + "@10", "cancel " + vpsBuilder.ID + "@10", "open " + started + "@0", "cancel " + started + "@0"}
			if got := streams.entries(); !slices.Equal(got, want) {
				t.Fatalf("streams=%v, want %v", got, want)
			}
		})
	}
}

func TestAHostSendAnsweredFromAnotherConversationStopsFollowing(t *testing.T) {
	moved := hosts.Qualify(vpsHostID, "remote-3")
	app, streams := followingHostSend(t, `{"evidence":"delivered","status":"sent","sessionId":"`+moved+`","turnId":"turn-9"}`)
	var output string
	stderr, err := captureStderr(t, func() error {
		var runErr error
		output, runErr = captureStdout(t, func() error {
			return app.Run([]string{"send", "vps::@builder", "task", "--reply", "--timeout", "3s"})
		})
		return runErr
	})
	if err != nil || !strings.Contains(stderr, "the coordinator moved to "+moved) || !strings.Contains(stderr, "agenthail last "+moved) || !strings.Contains(output, "turn-9") {
		t.Fatalf("output=%q stderr=%q err=%v, want the move named and the send not followed", output, stderr, err)
	}
	want := []string{"open " + vpsBuilder.ID + "@10", "cancel " + vpsBuilder.ID + "@10"}
	if got := streams.entries(); !slices.Equal(got, want) {
		t.Fatalf("streams=%v, want %v", got, want)
	}
}
