package surfacetest

import (
	"os"
	"path/filepath"
	"testing"
)

const FixtureNotionSpace = "11111111-2222-3333-4444-555555555555"

type cookieFixture struct {
	Bridge     string
	BridgeLog  string
	SidecarLog string
}

// The fake node stands in for the cookie bridge; the fake sidecar answers like
// app.notion.com. Both log what they were asked.
func NewCookieFixture(t *testing.T) cookieFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := cookieFixture{Bridge: filepath.Join(dir, "cookie.mjs"), BridgeLog: filepath.Join(dir, "bridge.log"), SidecarLog: filepath.Join(dir, "sidecar.log")}
	if err := os.WriteFile(fixture.Bridge, nil, 0600); err != nil {
		t.Fatal(err)
	}
	node := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + ShellQuote(fixture.BridgeLog) + "\nprintf 'sid=fixture'\n"
	InstallNode(t, node)
	sidecar := `import json, sys
req = json.loads(sys.stdin.read())
with open(` + PyQuote(fixture.SidecarLog) + `, "a") as log:
    log.write(json.dumps({"url": req["url"], "cookie": req["headers"].get("cookie", "")}) + "\n")
url = req["url"]
if url.endswith("/getSpaces"):
    body = {"user-1": {"notion_user": {"user-1": {"value": {"value": {"name": "Fixture User"}}}}, "space": {"` + FixtureNotionSpace + `": {}}}}
elif url.endswith("/getInferenceTranscriptsForUser"):
    body = {"transcripts": [{"id": "thread-1", "title": "Fixture thread", "updated_at": 1700000000000}]}
else:
    body = {}
sys.stdout.write(json.dumps({"status": 200, "body": json.dumps(body), "error": ""}))
`
	sidecarPath := filepath.Join(dir, "sidecar.py")
	if err := os.WriteFile(sidecarPath, []byte(sidecar), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SIDECAR", sidecarPath)
	return fixture
}
