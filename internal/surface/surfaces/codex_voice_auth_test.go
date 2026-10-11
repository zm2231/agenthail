package surfaces

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type voiceAuthClient struct {
	token     string
	onRequest func(map[string]any)
}

func (c *voiceAuthClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	if method != "getAuthStatus" {
		return nil, errors.New("unexpected auth method")
	}
	if c.onRequest != nil {
		c.onRequest(params)
	}
	return map[string]any{"result": map[string]any{"authMethod": "chatgpt", "authToken": c.token}}, nil
}
func (*voiceAuthClient) Close() error { return nil }

func voiceAuthToken(account string, expires time.Time) string {
	body, _ := json.Marshal(map[string]any{"exp": expires.Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": account}})
	return "header." + base64.RawURLEncoding.EncodeToString(body) + ".signature"
}

func writeVoiceAuth(t *testing.T, home, account, token string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{"access_token": token, "account_id": account}})
	if err := os.WriteFile(filepath.Join(home, "auth.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestValidStoredVoiceCredentialDoesNotForceRefresh(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	token := voiceAuthToken("account", now.Add(time.Hour))
	writeVoiceAuth(t, home, "account", token)
	opened := false
	c, err := resolveVoiceCredential(context.Background(), home, now, func(context.Context) (codexClient, error) {
		opened = true
		return nil, errors.New("Desktop token export unavailable")
	})
	if err != nil || opened || c.token != token || c.account != "account" {
		t.Fatal("valid existing OAuth login depended on Desktop export or refresh")
	}
}

func TestVoiceCredentialRenewalUsesCodexAndKeepsAccount(t *testing.T) {
	for _, mode := range []string{"exported", "persisted", "changed-account", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			now := time.Now()
			writeVoiceAuth(t, home, "account", voiceAuthToken("account", now.Add(-time.Hour)))
			account := "account"
			if mode == "changed-account" {
				account = "other"
			}
			fresh := voiceAuthToken(account, now.Add(time.Hour))
			client := &voiceAuthClient{}
			client.onRequest = func(params map[string]any) {
				if params["includeToken"] != true || params["refreshToken"] != true {
					t.Error("renewal not owned by Codex")
				}
				if mode == "persisted" {
					writeVoiceAuth(t, home, account, fresh)
				}
			}
			if mode == "exported" || mode == "changed-account" {
				client.token = fresh
			}
			c, err := resolveVoiceCredential(context.Background(), home, now, func(context.Context) (codexClient, error) { return client, nil })
			if mode == "changed-account" || mode == "unavailable" {
				if err == nil || strings.Contains(err.Error(), fresh) {
					t.Fatal("unusable or wrong-account renewal accepted")
				}
			} else if err != nil || c.token != fresh {
				t.Fatal("existing login renewal failed")
			}
		})
	}
}

func TestKeyringVoiceCredentialDoesNotRequireAuthFile(t *testing.T) {
	now := time.Now()
	client := &voiceAuthClient{token: voiceAuthToken("account", now.Add(time.Hour)), onRequest: func(params map[string]any) {
		if params["refreshToken"] != false {
			t.Error("unnecessary refresh")
		}
	}}
	c, err := resolveVoiceCredential(context.Background(), t.TempDir(), now, func(context.Context) (codexClient, error) { return client, nil })
	if err != nil || c.account != "account" {
		t.Fatal("Codex-managed non-file credential failed")
	}
}

func TestVoiceCredentialRejectsInvalidStoreWithoutSecretErrors(t *testing.T) {
	for _, body := range []string{`{"auth_mode":"api_key","OPENAI_API_KEY":"SECRET"}`, `{"auth_mode":"chatgpt","tokens":{"access_token":"SECRET"}}`, `SECRET`, strings.Repeat("SECRET", 12000)} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		opened := false
		_, err := resolveVoiceCredential(context.Background(), home, time.Now(), func(context.Context) (codexClient, error) { opened = true; return nil, errors.New("SECRET") })
		if err == nil || opened || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("invalid credential store leaked or switched authority")
		}
	}
	home := t.TempDir()
	now := time.Now()
	writeVoiceAuth(t, home, "other", voiceAuthToken("account", now.Add(time.Hour)))
	if _, err := storedVoiceCredential(home); err == nil {
		t.Fatal("inconsistent account identities accepted")
	}
}
