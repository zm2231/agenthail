package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), ManifestFile)
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestAManifestOffersItsPageAndMountsAsApps(t *testing.T) {
	manifest, err := readManifest(writeManifest(t, `{"label": "Board", "app": {"label": " Board ", "icon": "chart.bar.xaxis", "order": 2},
		"mounts": [{"name": "console", "label": "Console", "upstream": "http://127.0.0.1:9000", "app": {"icon": "terminal"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if app := manifest.App; app == nil || app.Label != "Board" || app.Icon != "chart.bar.xaxis" || app.Order != 2 {
		t.Fatalf("app %+v", manifest.App)
	}
	if app := manifest.Mounts[0].App; app == nil || app.Icon != "terminal" || app.Label != "" {
		t.Fatalf("mount app %+v", manifest.Mounts[0].App)
	}
	plain, err := readManifest(writeManifest(t, `{"label": "Plain"}`))
	if err != nil || plain.App != nil {
		t.Fatalf("a manifest without app read as %+v, %v", plain.App, err)
	}
}

func TestAManifestRefusesAnAppItCannotShow(t *testing.T) {
	for body, want := range map[string]string{
		`{"label": "X", "app": {"icon": "Chart Bar"}}`:                        "icon",
		`{"label": "X", "app": {"icon": "../x"}}`:                             "icon",
		`{"label": "X", "app": {"label": "` + strings.Repeat("a", 41) + `"}}`: "label",
		`{"label": "X", "app": {"order": 5000}}`:                              "order",
		`{"label": "X", "app": {"badge": 1}}`:                                 "unknown field",
		`{"label": "X", "mounts": [{"name": "m", "label": "M", "upstream": "http://127.0.0.1:1", "app": {"icon": "a..b"}}]}`: "icon",
	} {
		if _, err := readManifest(writeManifest(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error about %s", body, err, want)
		}
	}
}
