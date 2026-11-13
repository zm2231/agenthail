package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/poll"
)

const sourcesServer = `
import fs from 'node:fs';
import path from 'node:path';

export default function (ah: any) {
  const file = (name: string) => path.join(ah.dir, name);
  const counter = (name: string) => path.join(ah.dir, '..', '..', name);
  const count = (name: string) => {
    const runs = fs.existsSync(counter(name)) ? Number(fs.readFileSync(counter(name), 'utf8')) + 1 : 1;
    fs.writeFileSync(counter(name), String(runs));
    return runs;
  };
  ah.source({ name: 'fast', everySeconds: 600, collect: () => 'fast-value' });
  if (fs.existsSync(file('with-slow'))) ah.source({ name: 'slow', everySeconds: 60, timeoutSeconds: 30, collect: async () => {
    count('slow-runs');
    while (!fs.existsSync(file('release'))) await new Promise(resolve => setTimeout(resolve, 20));
    return 'slow-value';
  } });
  ah.source({ name: 'file', everySeconds: 600, collect: () => fs.readFileSync(file('value'), 'utf8') });
  ah.source({ name: 'stuck', everySeconds: 10, timeoutSeconds: 1, collect: () => new Promise(() => {}) });
  ah.source({ name: 'loads', everySeconds: 600, collect: () => count('runs') });
  ah.action('all', () => Object.fromEntries(Object.entries(ah.sources()).map(([name, s]: [string, any]) => [name, s.state])));
}
`

const sourcesPage = `<!doctype html><title>loading</title>
<p id="stamp"></p>
<script type="module">
  import { id, sources } from '/x/_ah.js';
  document.getElementById('stamp').textContent = Math.random().toString(36).slice(2);
  const seen = {};
  const show = name => state => {
    seen[name] = state.state + (state.value ? '=' + state.value : '');
    document.title = 'fast:' + seen.fast + ' slow:' + seen.slow;
  };
  sources({ fast: show('fast'), slow: show('slow') });
  fetch('/x/' + id + '/loaded', { cache: 'no-store' });
</script>`

type sourceView struct {
	Name         string          `json:"name"`
	State        string          `json:"state"`
	Value        json.RawMessage `json:"value"`
	At           int64           `json:"at"`
	Error        string          `json:"error"`
	ErrorAt      int64           `json:"errorAt"`
	EverySeconds int             `json:"everySeconds"`
}

func (f *extensionFixture) source(t *testing.T, name string) sourceView {
	t.Helper()
	response, body := f.request(t, http.MethodGet, "/x/fixture/s?name="+name, "secret", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("source %s answered %d %s", name, response.StatusCode, body)
	}
	var reply struct {
		Source sourceView `json:"source"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Source
}

func (f *extensionFixture) runJob(t *testing.T, name string) {
	t.Helper()
	f.request(t, http.MethodPost, "/api/v1/extensions/fixture/call", "secret", map[string]string{"job": name})
}

func TestASourceKeepsItsLastValueBesideTheErrorAndStopsAtItsTimeout(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"server.ts": sourcesServer, "value": "one"})
	eventually(t, "the file source is collected", func() bool { return f.source(t, "file").State == "ok" })
	if got := f.source(t, "file"); string(got.Value) != `"one"` || got.At == 0 {
		t.Fatalf("the file source holds %+v", got)
	}

	if err := os.WriteFile(filepath.Join(f.workdir, "value"), []byte(strings.Repeat("€", 200_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	f.runJob(t, "file")
	if big := f.source(t, "file"); big.State != "failed" || string(big.Value) != `"one"` || !strings.Contains(big.Error, "over the 524288-byte limit") {
		t.Fatalf("a value of 600 KB in UTF-8 but 200 K characters was not refused: state %s error %q", big.State, big.Error)
	}

	if err := os.Remove(filepath.Join(f.workdir, "value")); err != nil {
		t.Fatal(err)
	}
	f.runJob(t, "file")
	failed := f.source(t, "file")
	if failed.State != "failed" || string(failed.Value) != `"one"` || failed.Error == "" || failed.ErrorAt == 0 {
		t.Fatalf("a failed run should keep the last value beside its error: %+v", failed)
	}

	eventually(t, "the stuck source times out", func() bool { return f.source(t, "stuck").State == "failed" })
	if stuck := f.source(t, "stuck"); !strings.Contains(stuck.Error, "no answer in 1 s") {
		t.Fatalf("the stuck source failed with %q", stuck.Error)
	}

	response, body := f.request(t, http.MethodGet, "/x/fixture/s", "secret", nil)
	var list struct {
		Sources []sourceView `json:"sources"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &list) != nil || len(list.Sources) != 4 {
		t.Fatalf("the source list answered %d %s", response.StatusCode, body)
	}
	for _, listed := range list.Sources {
		if listed.Value != nil {
			t.Fatalf("the list carries values; pages fetch each source on its own: %s", body)
		}
	}
}

func TestACollectorThatIgnoresItsTimeoutHoldsTheSourceThroughAReload(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"server.ts": `
import fs from 'node:fs';
import path from 'node:path';

export default function (ah: any) {
  const outside = (name: string) => path.join(ah.dir, '..', '..', name);
  ah.source({ name: 'stuck', everySeconds: 10, timeoutSeconds: 1, collect: async () => {
    const runs = fs.existsSync(outside('runs')) ? Number(fs.readFileSync(outside('runs'), 'utf8')) + 1 : 1;
    fs.writeFileSync(outside('runs'), String(runs));
    while (!fs.existsSync(outside('release'))) await new Promise(resolve => setTimeout(resolve, 20));
    return runs;
  } });
}
`})
	outside := filepath.Dir(filepath.Dir(f.workdir))
	runs := func() string {
		got, _ := os.ReadFile(filepath.Join(outside, "runs"))
		return string(got)
	}
	eventually(t, "the stuck source times out", func() bool { return f.source(t, "stuck").State == "failed" })
	if _, body := f.request(t, http.MethodPost, "/api/v1/extensions/fixture/call", "secret", map[string]string{"job": "stuck"}); !strings.Contains(string(body), "has not finished") {
		t.Fatalf("a run beside the one still going answered %s", body)
	}
	if err := f.d.reloadExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if got := runs(); got != "1" {
		t.Fatalf("the reloaded source started beside the collector still going: %s runs", got)
	}
	if err := os.WriteFile(filepath.Join(outside, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := poll.Until(ctx, 50*time.Millisecond, func() (bool, error) { return runs() == "2", nil }); err != nil {
		t.Fatalf("the reloaded source did not run once the old collector settled, well before its 10 s interval: %s runs", runs())
	}
}

func TestEverySourceRunsAgainWhenTheExtensionReloads(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"server.ts": sourcesServer, "value": "one"})
	eventually(t, "the loads source runs once the extension loads", func() bool { return string(f.source(t, "loads").Value) == "1" })
	if err := f.d.reloadExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the loads source runs again after the reload, long before its 600 s interval", func() bool {
		return string(f.source(t, "loads").Value) == "2"
	})
}

func TestASourceReloadedMidRunRunsTheNewCodeAsSoonAsTheOldRunEnds(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"server.ts": sourcesServer, "value": "one", "with-slow": ""})
	runs := func() string {
		got, _ := os.ReadFile(filepath.Join(f.workdir, "..", "..", "slow-runs"))
		return string(got)
	}
	eventually(t, "the slow source starts", func() bool { return runs() == "1" })
	if err := f.d.reloadExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.workdir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the reloaded source runs again right after the old run, not 60 s later", func() bool { return runs() == "2" })
}

func TestASourceMustRunAtLeastTenSecondsApartAndTimeOutWithinItsInterval(t *testing.T) {
	for name, server := range map[string]string{
		"too often":        `export default function (ah: any) { ah.source({ name: 'cpu', everySeconds: 5, collect: () => 1 }); }`,
		"timeout too long": `export default function (ah: any) { ah.source({ name: 'cpu', everySeconds: 10, timeoutSeconds: 10, collect: () => 1 }); }`,
	} {
		t.Run(name, func(t *testing.T) {
			home := extensionTestHome(t)
			dir := filepath.Join(extension.Workspace(home), "fixture")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for file, body := range map[string]string{"extension.json": `{"label": "Fixture"}`, "server.ts": server} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f := startExtensionDaemon(t, home, extensionDaemonOptions{})
			f.awaitState(t, "failed")
			if got := f.d.extensionsView().Extensions[0].Error; !strings.Contains(got, "source cpu") {
				t.Fatalf("the extension failed with %q", got)
			}
		})
	}
}

func TestAPageDrawsEachSourceAsItLandsWithoutWaitingForTheSlowest(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"server.ts": sourcesServer, "ui.html": sourcesPage, "value": "one"})
	browser := signIn(t, f)
	agent := browserAgent(t, f)
	if err := os.WriteFile(filepath.Join(f.workdir, "with-slow"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.d.reloadExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	tab := browse(t, agent, map[string]any{"action": "open", "url": browser.URL + "/x/fixture/"})["tab"].(string)
	snapshot := func() (string, string) {
		got := browse(t, agent, map[string]any{"action": "snapshot", "tab": tab})
		text := browse(t, agent, map[string]any{"action": "text", "tab": tab})
		return got["title"].(string), text["text"].(string)
	}
	var title string
	eventually(t, "the fast source draws while the slow one is still collecting", func() bool {
		title, _ = snapshot()
		return title == `fast:ok=fast-value slow:collecting`
	})
	_, before := snapshot()

	if err := os.WriteFile(filepath.Join(f.workdir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the slow source fills in once it lands", func() bool {
		title, _ = snapshot()
		return title == `fast:ok=fast-value slow:ok=slow-value`
	})
	if _, after := snapshot(); after != before {
		t.Fatalf("the page reloaded to show the slow source: %q then %q", before, after)
	}
}
