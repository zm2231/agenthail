package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorFlagsWhatAnInterruptedPackageInstallLeftOnTheBareExecutable(t *testing.T) {
	dir := t.TempDir()
	install := packageInstall{
		daemon:      filepath.Join(dir, bundledDaemon),
		bare:        filepath.Join(dir, "agenthail"),
		launcher:    filepath.Join(dir, "launcher"),
		launchAgent: filepath.Join(dir, "com.agenthail.daemon.plist"),
	}
	agent := func(program string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>com.agenthail.daemon</string><key>ProgramArguments</key><array><string>` + program + `</string><string>daemon-run</string></array></dict></plist>`
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if problems := packageProblems(install); problems != nil {
		t.Fatalf("an install without the package reports %+v", problems)
	}
	write(install.daemon, "")
	write(install.bare, "")
	write(install.launcher, "#!/bin/bash\nexec \"$AGENTHAIL_ROOT/agenthail\" \"$@\"\n")
	write(install.launchAgent, agent(install.bare))
	var kinds []string
	for _, problem := range packageProblems(install) {
		kinds = append(kinds, problem.Problem)
		if problem.Fix == "" || !strings.Contains(problem.Detail, dir) {
			t.Fatalf("problem without a fix or a path: %+v", problem)
		}
	}
	if got := strings.Join(kinds, ","); got != "stale_launch_agent,stale_launcher,stale_daemon_executable" {
		t.Fatalf("problems = %s", got)
	}
	os.Remove(install.bare)
	write(install.launcher, "#!/bin/bash\nexec \"$AGENTHAIL_ROOT/"+bundledDaemon+"\" \"$@\"\n")
	write(install.launchAgent, agent(install.daemon))
	if problems := packageProblems(install); len(problems) != 0 {
		t.Fatalf("a complete install reports %+v", problems)
	}
}
