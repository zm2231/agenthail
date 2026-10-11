package surfaces

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/voice"
)

func TestVoiceCallUsesClientDelegationWithoutCodingSideband(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("openai-alpha") != "quicksilver=v2" || r.Header.Get("chatgpt-account-id") != "account" {
			t.Error("missing voice authentication contract")
		}
		var body struct {
			SDP     string `json:"sdp"`
			Session struct {
				Model        string `json:"model"`
				Instructions string `json:"instructions"`
				Delegation   struct {
					Type string `json:"type"`
					Ack  bool   `json:"ack_filler"`
				} `json:"delegation"`
				Initial []map[string]any `json:"initial_items"`
			} `json:"session"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.SDP != "v=0 offer" || body.Session.Model != "gpt-live-1-codex" || body.Session.Instructions != "prompt" || body.Session.Delegation.Type != "client" || body.Session.Delegation.Ack {
			t.Error("invalid host-owned call setup")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0 answer"))
	}))
	defer server.Close()
	answer, err := setupVoiceCall(context.Background(), server.Client(), server.URL, "fixture-secret", "account", voice.CallRequest{AttemptID: "call", SDP: "v=0 offer", Instructions: "prompt"})
	if err != nil || answer != "v=0 answer" {
		t.Fatalf("answer=%q error=%v", answer, err)
	}
}

func TestVoiceCallRejectsRedirectsAndBoundsProviderBody(t *testing.T) {
	for _, mode := range []string{"redirect", "oversize", "invalid", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				switch mode {
				case "redirect":
					http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
				case "oversize":
					w.WriteHeader(201)
					_, _ = w.Write([]byte("v=0" + strings.Repeat("x", 96<<10)))
				case "invalid":
					w.WriteHeader(201)
					_, _ = w.Write([]byte("fixture-secret"))
				case "rejected":
					w.WriteHeader(401)
					_, _ = w.Write([]byte("fixture-secret"))
				}
			}))
			defer server.Close()
			_, err := setupVoiceCall(context.Background(), server.Client(), server.URL, "fixture-secret", "account", voice.CallRequest{})
			if err == nil || strings.Contains(err.Error(), "fixture-secret") || hits != 1 {
				t.Fatalf("unsafe setup failure: hits=%d error=%v", hits, err)
			}
		})
	}
}

func TestVoiceAccountReadsCodexAccountClaim(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"fixture-account"}}`))
	account, err := voiceAccount("header." + claims + ".signature")
	if err != nil || account != "fixture-account" {
		t.Fatalf("account=%q error=%v", account, err)
	}
	if _, err = voiceAccount("not-a-login"); err == nil {
		t.Fatal("accepted a missing Codex login")
	}
}

func TestLiveCodexVoiceLoginCapability(t *testing.T) {
	if os.Getenv("AGENTHAIL_VERIFY_VOICE_LOGIN") != "1" {
		t.Skip("explicit read-only login verification required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := NewCodex("").openDesktop(ctx)
	if err != nil {
		t.Fatal("desktop login connection unavailable")
	}
	defer client.Close()
	response, err := client.Request(ctx, "getAuthStatus", map[string]any{"includeToken": true, "refreshToken": false}, 10*time.Second)
	if err != nil {
		t.Fatal("desktop does not expose the required login capability")
	}
	result, _ := response["result"].(map[string]any)
	token, _ := result["authToken"].(string)
	if _, err = voiceAccount(token); err != nil {
		t.Fatal("desktop login does not provide a usable voice account")
	}
	t.Log("Codex login capability verified; credentials not printed, no model call started")
}
