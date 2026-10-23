package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func daemonBuildFixture(t *testing.T, version string) *App {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, version)
	}))
	t.Cleanup(server.Close)
	app := &App{Version: "v0.2.69", Revision: "abcd1234abcd1234", BuiltAt: "2026-10-23T12:00:00Z"}
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	return app
}

func TestDoctorNamesBothBuildsAndFlagsOnlyADifferentKnownOne(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
		flagged bool
		detail  string
	}{
		{"another commit", `{"protocol":1,"version":"v0.2.68","revision":"e387b0d87b52f557","modified":false}`, true, "the running daemon is v0.2.68 (e387b0d8), this agenthail is v0.2.69 (abcd1234)"},
		{"the same commit", `{"protocol":1,"version":"v0.2.69","revision":"abcd1234abcd1234","modified":false}`, false, "the running daemon is v0.2.69 (abcd1234), this agenthail is v0.2.69 (abcd1234)"},
		{"a daemon from a modified tree", `{"protocol":1,"version":"dev","revision":"e387b0d87b52f557","modified":true}`, false, "the running daemon is dev (e387b0d8, modified), this agenthail is v0.2.69 (abcd1234)"},
		{"a daemon that does not name its build", `{"protocol":1}`, false, "the running daemon is an older build (unknown), this agenthail is v0.2.69 (abcd1234)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, problem := daemonBuildFixture(t, test.version).checkDaemonBuild(context.Background())
			if report == nil || report.Detail != test.detail {
				t.Fatalf("report=%+v", report)
			}
			if (problem != nil) != test.flagged {
				t.Fatalf("problem=%+v, want flagged=%v", problem, test.flagged)
			}
			if problem != nil && (problem.Problem != "daemon_build_mismatch" || problem.Detail != test.detail || problem.Fix != "agenthail daemon restart") {
				t.Fatalf("problem=%+v", problem)
			}
		})
	}
}

func TestDoctorSkipsTheBuildCheckWithNoDaemon(t *testing.T) {
	app := &App{Version: "v0.2.69", Revision: "abcd1234"}
	app.catalogDaemonRunning = func() bool { return false }
	if report, problem := app.checkDaemonBuild(context.Background()); report != nil || problem != nil {
		t.Fatalf("report=%+v problem=%+v", report, problem)
	}
}

func TestDoctorSaysWhenTheDaemonBuildCannotBeRead(t *testing.T) {
	app := daemonBuildFixture(t, `not json`)
	report, problem := app.checkDaemonBuild(context.Background())
	if problem != nil || report == nil || report.Error == "" || !strings.HasSuffix(report.Detail, "this agenthail is v0.2.69 (abcd1234)") {
		t.Fatalf("report=%+v problem=%+v", report, problem)
	}
}
