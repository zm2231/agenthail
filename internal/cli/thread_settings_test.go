package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestThreadSettingsShowsWhereEachSettingCameFromAndWhatIsUnknown(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	if err := store.RegisterSession(surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir(), Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlias("builder", "s1"); err != nil {
		t.Fatal(err)
	}
	body := `{"sessionId":"s1","surface":"claude","configRoot":{"name":"work","root":"/home/me/.work/.claude","source":"config","missing":true},"model":{"value":"claude-private","source":"transcript","listed":false},"effort":null,"permission":{"mode":"acceptEdits","source":"transcript"},"ceiling":{"permission":"edits"},"unavailable":{"effort":"only a session the Claude host runs reports its effort"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/session-settings" || r.URL.Query().Get("id") != "s1" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, httpClient: server.Client()}, nil
	}

	out, err := captureStdout(t, func() error { return app.Run([]string{"thread", "settings", "@builder"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"model        claude-private [custom]  (transcript)",
		"permission   acceptEdits  (transcript)",
		"config root  work  /home/me/.work/.claude  [does not exist]  (from claudeConfigDirs)",
		"ceiling      edits",
		"effort       unknown: only a session the Claude host runs reports its effort",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	out, err = captureStdout(t, func() error { return app.Run([]string{"thread", "settings", "s1", "--json"}) })
	if err != nil || !strings.Contains(out, `"unavailable":{"effort"`) {
		t.Fatalf("json=%s err=%v", out, err)
	}
}
