package surfaces

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type voiceCredential struct {
	token, account string
	expires        time.Time
}

var errVoiceCredentialNotStored = errors.New("Codex OAuth credential is not stored in a file")

func parseVoiceCredential(token string) (voiceCredential, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return voiceCredential{}, errors.New("Codex did not provide an OAuth access token for voice")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Expires int64 `json:"exp"`
		Auth    struct {
			Account string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err != nil || json.Unmarshal(body, &claims) != nil || claims.Auth.Account == "" {
		return voiceCredential{}, errors.New("Codex voice credential has no usable account identity")
	}
	return voiceCredential{token: token, account: claims.Auth.Account, expires: time.Unix(claims.Expires, 0)}, nil
}

func storedVoiceCredential(home string) (voiceCredential, error) {
	f, err := os.Open(filepath.Join(home, "auth.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return voiceCredential{}, errVoiceCredentialNotStored
		}
		return voiceCredential{}, errors.New("Codex stored voice credential is unavailable")
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			Access  string `json:"access_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if err != nil || len(body) > 64<<10 || json.Unmarshal(body, &auth) != nil {
		return voiceCredential{}, errors.New("Codex stored voice credential could not be read")
	}
	if auth.Mode != "chatgpt" {
		return voiceCredential{}, errors.New("The configured Codex credential is not a ChatGPT OAuth login")
	}
	c, err := parseVoiceCredential(auth.Tokens.Access)
	if err != nil {
		return voiceCredential{}, err
	}
	if auth.Tokens.Account != "" && c.account != auth.Tokens.Account {
		return voiceCredential{}, errors.New("Codex stored voice account identities disagree")
	}
	return c, nil
}

func resolveVoiceCredential(ctx context.Context, home string, now time.Time, open func(context.Context) (codexClient, error)) (voiceCredential, error) {
	stored, storedErr := storedVoiceCredential(home)
	if storedErr != nil && !errors.Is(storedErr, errVoiceCredentialNotStored) {
		return voiceCredential{}, storedErr
	}
	if storedErr == nil && stored.expires.After(now.Add(time.Minute)) {
		return stored, nil
	}
	client, err := open(ctx)
	if err != nil {
		return voiceCredential{}, errors.New("Codex voice credential manager is unavailable")
	}
	defer client.Close()
	// Codex owns OAuth refresh and persistence; Agenthail never rotates tokens.
	response, err := client.Request(ctx, "getAuthStatus", map[string]any{"includeToken": true, "refreshToken": storedErr == nil}, 10*time.Second)
	if err != nil {
		return voiceCredential{}, errors.New("Codex could not resolve its existing voice credential")
	}
	result, _ := response["result"].(map[string]any)
	token, _ := result["authToken"].(string)
	c, err := parseVoiceCredential(token)
	if err == nil && c.expires.After(now.Add(time.Minute)) {
		if storedErr == nil && c.account != stored.account {
			return voiceCredential{}, errors.New("Codex voice refresh changed the selected account")
		}
		return c, nil
	}
	// A refreshed login can be persisted without being exported by Desktop.
	c, err = storedVoiceCredential(home)
	if err == nil && c.expires.After(now.Add(time.Minute)) {
		if storedErr == nil && c.account != stored.account {
			return voiceCredential{}, errors.New("Codex voice refresh changed the selected account")
		}
		return c, nil
	}
	return voiceCredential{}, errors.New("Codex could not supply a usable voice credential from the existing login; no audio call was started")
}
