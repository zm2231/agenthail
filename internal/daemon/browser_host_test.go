package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/browserhost"
	"github.com/zm2231/agenthail/internal/testnode"
)

func TestTheSnapshotReportsTheBrowserHostAndChangesWhenItStarts(t *testing.T) {
	d, _, _, _, handler, _ := baseFixture(t)
	home, err := os.MkdirTemp("", "bh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	_, file, _, _ := runtime.Caller(0)
	launch := browserhost.Launch{Node: testnode.Path(t), Script: filepath.Join(filepath.Dir(file), "..", "..", "browser-host", "host.mjs"), Version: "test", Env: os.Environ()}
	d.SetBrowserHost(&browserhost.Manager{Home: home, Launch: func() (browserhost.Launch, error) { return launch, nil }})

	read := func() (browserhost.Status, string) {
		response := getSnapshot(t, handler, "/api/v1/snapshot", "")
		var body struct {
			Daemon struct {
				Browser *browserhost.Status `json:"browser"`
			} `json:"daemon"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Daemon.Browser == nil {
			t.Fatalf("snapshot daemon.browser missing (%v): %s", err, response.Body.String())
		}
		return *body.Daemon.Browser, response.Header().Get("ETag")
	}
	before, beforeTag := read()
	if before.State != browserhost.StateUnavailable {
		t.Fatalf("before the host starts: %+v", before)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.superviseBrowserHost(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		after, afterTag := read()
		if after.State == browserhost.StateAbsent {
			if after.Version == "" || after.Running || afterTag == beforeTag {
				t.Fatalf("started host: %+v tag %s (was %s)", after, afterTag, beforeTag)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshot still reports %+v", after)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
