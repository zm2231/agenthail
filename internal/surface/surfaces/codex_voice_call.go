package surfaces

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

// Call setup uses the Codex login manager, but does not attach a coding thread
// as a realtime sideband. Delegations arrive only through the phone channel.
func (v *CodexVoice) StartCall(ctx context.Context, call voice.CallRequest) (string, error) {
	client, err := v.Codex.openDesktop(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()
	response, err := client.Request(ctx, "getAuthStatus", map[string]any{"includeToken": true, "refreshToken": true}, 10*time.Second)
	if err != nil {
		return "", errors.New("Codex voice login is unavailable; renew the Codex login")
	}
	result, _ := response["result"].(map[string]any)
	token, _ := result["authToken"].(string)
	account, err := voiceAccount(token)
	if err != nil {
		return "", err
	}
	return setupVoiceCall(ctx, http.DefaultClient, "https://chatgpt.com/backend-api/codex/realtime/calls?intent=quicksilver&architecture=avas", token, account, call)
}

func voiceAccount(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("voice requires a ChatGPT Codex login")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Auth struct {
			Account string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err != nil || json.Unmarshal(body, &claims) != nil || claims.Auth.Account == "" {
		return "", errors.New("Codex voice account is unavailable; renew the Codex login")
	}
	return claims.Auth.Account, nil
}

func setupVoiceCall(ctx context.Context, client *http.Client, endpoint, token, account string, call voice.CallRequest) (string, error) {
	body, err := json.Marshal(map[string]any{"sdp": call.SDP, "session": map[string]any{
		"model": "gpt-live-1-codex", "instructions": call.Instructions,
		"audio":         map[string]any{"output": map[string]any{"voice": "cove"}},
		"delegation":    map[string]any{"type": "client", "ack_filler": false},
		"initial_items": call.InitialItems,
	}})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("chatgpt-account-id", account)
	request.Header.Set("openai-alpha", "quicksilver=v2")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("originator", "agenthail")
	request.Header.Set("x-session-id", call.AttemptID)
	request.Header.Set("User-Agent", "Agenthail")
	// Login credentials must never follow a redirect, including same-host redirects.
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copy.Do(request)
	if err != nil {
		return "", errors.New("voice call setup could not reach the provider")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("voice call setup rejected (HTTP %d)", response.StatusCode)
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, (96<<10)+1))
	if err != nil || len(answer) > 96<<10 || !bytes.HasPrefix(answer, []byte("v=0")) {
		return "", errors.New("voice provider returned an invalid audio answer")
	}
	return string(answer), nil
}

func (v *CodexVoice) Execute(ctx context.Context, session *surface.Session, input string) (string, error) {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return "", err
	}
	defer releaseCodexWriteLock(lock)
	client, err := v.Codex.openSession(ctx, session, true)
	if err != nil {
		return "", err
	}
	defer client.Close()
	active, err := v.Codex.activeTurnID(ctx, client, session.ID)
	if err != nil {
		return "", err
	}
	if active != "" {
		return "", errors.New("voice executor already has an active turn; no request was submitted")
	}
	params := map[string]any{"threadId": session.ID, "input": []map[string]any{{"type": "text", "text": input}}}
	method := "turn/start"
	response, err := client.Request(ctx, method, params, 10*time.Second)
	if err != nil {
		return "", errors.New("voice execution submission could not be confirmed; the request was not retried")
	}
	result, _ := response["result"].(map[string]any)
	turn, _ := result["turn"].(map[string]any)
	id := str(turn, "id")
	if id == "" {
		return "", errors.New("voice execution returned no turn identity")
	}
	return id, nil
}
