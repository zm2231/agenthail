//go:build darwin

package permissions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bundle lays out a daemon bundle whose reporter is the given shell script,
// and returns the daemon executable's path.
func bundle(t *testing.T, script string) string {
	t.Helper()
	macOS := filepath.Join(t.TempDir(), "Agenthail Daemon.app", "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macOS, Reporter), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(macOS, "agenthail")
}

func TestCheckReportsEachGrantWithItsSettingsPane(t *testing.T) {
	exe := bundle(t, `test "$1" = com.openai.sky.CUAService || exit 3
echo '{"accessibility":"granted","screenRecording":"missing","automation":{"com.openai.sky.CUAService":"denied"}}'`)
	report := Check(context.Background(), Daemon{Executable: exe, Service: Service})
	if report.Problem != "" || !report.Supported || report.Holder != filepath.Dir(filepath.Dir(filepath.Dir(exe))) {
		t.Fatalf("report = %+v", report)
	}
	want := []Grant{
		{Name: "Accessibility", State: Granted, Pane: "System Settings > Privacy & Security > Accessibility", URL: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"},
		{Name: "Screen Recording", State: Missing, Pane: "System Settings > Privacy & Security > Screen & System Audio Recording", URL: "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"},
		{Name: "Automation", Target: "com.openai.sky.CUAService", TargetName: "Codex Computer Use", State: Denied, Pane: "System Settings > Privacy & Security > Automation", URL: "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation"},
	}
	if len(report.Grants) != len(want) {
		t.Fatalf("grants = %+v", report.Grants)
	}
	for i := range want {
		if report.Grants[i] != want[i] {
			t.Errorf("grant %d = %+v, want %+v", i, report.Grants[i], want[i])
		}
	}
	if got := report.Grants[2].Describe(); got != "Automation of Codex Computer Use: denied; turn on Agenthail Daemon in System Settings > Privacy & Security > Automation" {
		t.Errorf("Describe = %q", got)
	}
}

func TestCheckOutsideTheBundleSaysWhyItCannotRead(t *testing.T) {
	report := Check(context.Background(), Daemon{Executable: "/usr/local/libexec/agenthail", Service: Service})
	if !report.Supported || report.Holder != "" || len(report.Grants) != 0 || !strings.Contains(report.Problem, "outside the Agenthail Daemon bundle") {
		t.Fatalf("report = %+v", report)
	}
}

// A daemon launchd did not start inherits whatever started it, a terminal
// say, whose grants the reporter would read under the bundle's name.
func TestCheckOfADaemonLaunchdDidNotStartReadsNothing(t *testing.T) {
	exe := bundle(t, `echo '{"accessibility":"granted","screenRecording":"granted","automation":{}}'`)
	for _, service := range []string{"", "application.com.apple.Terminal.123"} {
		report := Check(context.Background(), Daemon{Executable: exe, Service: service})
		if report.Holder != "" || len(report.Grants) != 0 || !strings.Contains(report.Problem, "launchd did not start the daemon as com.agenthail.daemon") {
			t.Fatalf("service %q: report = %+v", service, report)
		}
		if err := Request(Daemon{Executable: exe, Service: service}); err == nil {
			t.Fatalf("service %q: a request was made", service)
		}
	}
}

func TestCheckReportsAReporterThatFails(t *testing.T) {
	report := Check(context.Background(), Daemon{Executable: bundle(t, "echo broken >&2; exit 1"), Service: Service})
	if len(report.Grants) != 0 || !strings.Contains(report.Problem, "broken") {
		t.Fatalf("report = %+v", report)
	}
}

func TestRequestAsksThroughTheReporter(t *testing.T) {
	dir := t.TempDir()
	exe := bundle(t, `echo "$@" > `+filepath.Join(dir, "asked"))
	if err := Request(Daemon{Executable: exe, Service: Service}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if data, err := os.ReadFile(filepath.Join(dir, "asked")); err == nil && len(data) > 0 {
			if got := strings.TrimSpace(string(data)); got != "--ask com.openai.sky.CUAService" {
				t.Fatalf("asked with %q", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the reporter was not run")
}
