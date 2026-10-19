package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
)

type fakeService struct {
	loaded   bool
	problems []serviceProblem
}

func (f *fakeService) Name() string               { return "fake" }
func (f *fakeService) Loaded() bool               { return f.loaded }
func (f *fakeService) Check() error               { return nil }
func (f *fakeService) Install() error             { return nil }
func (f *fakeService) Uninstall() error           { return nil }
func (f *fakeService) Start() error               { return nil }
func (f *fakeService) Stop() error                { return nil }
func (f *fakeService) Restart() error             { return nil }
func (f *fakeService) StatusLines() []string      { return nil }
func (f *fakeService) Problems() []serviceProblem { return f.problems }

type fakeSystemctl struct {
	t             *testing.T
	mu            sync.Mutex
	daemonScript  string
	calls         []string
	reloaded      bool
	unitPath      string
	process       *exec.Cmd
	managerDown   bool
	linger        bool
	lingerRefused bool
}

func newFakeSystemctl(t *testing.T) (*fakeSystemctl, *systemdService) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("AGENTHAIL_CLAUDE_BIN", "")
	bin := filepath.Join(t.TempDir(), "agenthail")
	script := "#!/bin/sh\nmkdir -p \"$HOME/.agenthail\"\necho $$ > \"$HOME/.agenthail/daemon.pid\"\ntrap 'exit 0' TERM\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSystemctl{t: t, daemonScript: bin, unitPath: filepath.Join(home, ".config", "systemd", "user", systemdUnitName)}
	service := &systemdService{run: fake.run, unitPath: fake.unitPath, user: "agent", uid: 1000, home: home}
	t.Cleanup(fake.stopDaemon)
	return fake, service
}

func (f *fakeSystemctl) startDaemon() {
	cmd := exec.Command(f.daemonScript, "daemon-run")
	if err := cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	f.process = cmd
	if _, ok := waitForDaemonPID(0, 5e9); !ok {
		f.t.Fatal("fake daemon did not start")
	}
}

func (f *fakeSystemctl) stopDaemon() {
	if f.process == nil {
		return
	}
	_ = f.process.Process.Signal(syscall.SIGTERM)
	_ = f.process.Wait()
	f.process = nil
}

func (f *fakeSystemctl) run(name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case name == "loginctl" && args[0] == "show-user":
		if f.linger {
			return []byte("yes\n"), nil
		}
		return []byte("no\n"), nil
	case name == "loginctl" && args[0] == "enable-linger":
		if f.lingerRefused {
			return []byte("Could not enable linger: Access denied\n"), errors.New("exit status 1")
		}
		f.linger = true
		return nil, nil
	case name != "systemctl" || args[0] != "--user":
		f.t.Fatalf("unexpected command %s", call)
	}
	if f.managerDown {
		return []byte("Failed to connect to bus: No medium found\n"), errors.New("exit status 1")
	}
	switch strings.Join(args[1:], " ") {
	case "show --property=Version":
		return []byte("255\n"), nil
	case "show " + systemdUnitName + " --property=LoadState --value":
		if _, err := os.Stat(f.unitPath); err == nil && f.reloaded {
			return []byte("loaded\n"), nil
		}
		return []byte("not-found\n"), nil
	case "daemon-reload":
		f.reloaded = true
	case "enable " + systemdUnitName:
	case "disable --now " + systemdUnitName:
		f.stopDaemon()
	case "start " + systemdUnitName:
		if f.process == nil {
			f.startDaemon()
		}
	case "restart " + systemdUnitName:
		f.stopDaemon()
		f.startDaemon()
	case "stop " + systemdUnitName:
		f.stopDaemon()
	case "is-active " + systemdUnitName:
		if f.process != nil {
			return []byte("active\n"), nil
		}
		return []byte("inactive\n"), errors.New("exit status 3")
	default:
		f.t.Fatalf("unexpected command %s", call)
	}
	return nil, nil
}

func (f *fakeSystemctl) systemctlCalls() []string {
	var calls []string
	for _, call := range f.calls {
		if strings.HasPrefix(call, "systemctl --user ") {
			action := strings.TrimPrefix(call, "systemctl --user ")
			if !strings.HasPrefix(action, "show ") && !strings.HasPrefix(action, "is-active ") {
				calls = append(calls, action)
			}
		}
	}
	return calls
}

func TestSystemdUnitKeepsAgentsAcrossRestartAndEscapesValues(t *testing.T) {
	unit, err := systemdUnit("/opt/agent hail/$bin/agenthail", map[string]string{
		"PATH":                        "/home/agent/.local/bin:/usr/bin",
		"CODEX_HOME":                  `/home/agent/codex "100%"`,
		"AGENTHAIL_DAEMON_SUPERVISOR": "systemd",
		"AGENTHAIL_DEBUG":             "",
	}, "/home/agent/.agenthail/daemon.log")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`ExecStart="/opt/agent hail/$$bin/agenthail" daemon-run`,
		"KillMode=process",
		"StartLimitIntervalSec=0",
		"Restart=always",
		"StandardOutput=append:/home/agent/.agenthail/daemon.log",
		"StandardError=append:/home/agent/.agenthail/daemon.log",
		"WantedBy=default.target",
		`Environment="CODEX_HOME=/home/agent/codex \"100%%\""`,
		`Environment="AGENTHAIL_DAEMON_SUPERVISOR=systemd"`,
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "AGENTHAIL_DEBUG") {
		t.Fatalf("empty environment value written:\n%s", unit)
	}
	environment := systemdUnitEnvironment(unit)
	if environment["CODEX_HOME"] != `/home/agent/codex "100%"` || environment["PATH"] != "/home/agent/.local/bin:/usr/bin" {
		t.Fatalf("environment=%v", environment)
	}
	if _, err := systemdUnit("/opt/agenthail", map[string]string{"PATH": "/usr/bin\nExecStartPre=/bin/false"}, "/tmp/log"); err == nil {
		t.Fatal("newline in an environment value was accepted")
	}
}

func TestSystemdInstallWritesUnitStartsDaemonAndEnablesLinger(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	output, err := captureStdout(t, service.Install)
	if err != nil {
		t.Fatalf("install: %v output=%s", err, output)
	}
	if got, want := strings.Join(fake.systemctlCalls(), "|"), "daemon-reload|enable "+systemdUnitName+"|restart "+systemdUnitName; got != want {
		t.Fatalf("systemctl calls=%q, want %q", got, want)
	}
	unit, err := os.ReadFile(fake.unitPath)
	if err != nil {
		t.Fatal(err)
	}
	environment := systemdUnitEnvironment(string(unit))
	if environment["AGENTHAIL_DAEMON_SUPERVISOR"] != "systemd" || environment["CODEX_HOME"] != os.Getenv("CODEX_HOME") || environment["PATH"] != os.Getenv("PATH") {
		t.Fatalf("unit environment=%v", environment)
	}
	if !fake.linger || !strings.Contains(output, "linger: on") || !strings.Contains(output, "installed and started systemd user service") {
		t.Fatalf("linger=%v output=%s", fake.linger, output)
	}
	config, err := daemon.LoadDashboardConfig()
	if err != nil || !config.Enabled {
		t.Fatalf("dashboard config=%+v err=%v", config, err)
	}
	if !service.Loaded() {
		t.Fatal("installed unit is not loaded")
	}
}

func TestSystemdInstallRefusesUnreachableUserManager(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	fake.managerDown = true
	err := service.Install()
	if err == nil || !strings.Contains(err.Error(), "loginctl enable-linger agent") || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR=/run/user/1000") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(fake.unitPath); !os.IsNotExist(statErr) {
		t.Fatalf("unit written although the user manager is unreachable: %v", statErr)
	}
}

func TestSystemdInstallRefusesCodexHomeThatCannotExecute(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	blocked := filepath.Join(t.TempDir(), "codex-home")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", blocked)
	err := service.Install()
	if err == nil || !strings.Contains(err.Error(), blocked+" cannot run executables") || !strings.Contains(err.Error(), "rerun 'agenthail daemon install' with CODEX_HOME set") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(fake.unitPath); !os.IsNotExist(statErr) {
		t.Fatal("unit written for a CODEX_HOME that cannot execute")
	}
}

func TestSystemdLifecycleCommandsUseTheUserUnit(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	if _, err := captureStdout(t, service.Install); err != nil {
		t.Fatal(err)
	}
	firstPID, _ := daemon.IsRunning()
	output, err := captureStdout(t, service.Restart)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	secondPID, running := daemon.IsRunning()
	if !running || secondPID == firstPID || !strings.Contains(output, "daemon restarted") {
		t.Fatalf("first=%d second=%d output=%s", firstPID, secondPID, output)
	}
	if err := service.Start(); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("start while running err=%v", err)
	}
	output, err = captureStdout(t, service.Stop)
	if err != nil || !strings.Contains(output, "agents it started keep running") {
		t.Fatalf("stop err=%v output=%s", err, output)
	}
	if _, running := daemon.IsRunning(); running {
		t.Fatal("daemon still running after stop")
	}
	if _, err := captureStdout(t, service.Start); err != nil {
		t.Fatalf("start: %v", err)
	}
	if lines := strings.Join(service.StatusLines(), "\n"); !strings.Contains(lines, "service: systemd agenthail.service (active)") || !strings.Contains(lines, "linger: on") {
		t.Fatalf("status lines=%q", lines)
	}
	fake.calls = nil
	if _, err := captureStdout(t, service.Uninstall); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(fake.systemctlCalls(), "|"), "disable --now "+systemdUnitName+"|daemon-reload"; got != want {
		t.Fatalf("uninstall calls=%q, want %q", got, want)
	}
	if _, err := os.Stat(fake.unitPath); !os.IsNotExist(err) {
		t.Fatal("unit file left after uninstall")
	}
}

func TestSystemdProblemsNameTheExactFix(t *testing.T) {
	_, service := newFakeSystemctl(t)
	t.Setenv("PATH", t.TempDir())
	problems := service.Problems()
	byName := map[string]serviceProblem{}
	for _, problem := range problems {
		byName[problem.Problem] = problem
	}
	if byName["linger-off"].Fix != "run 'loginctl enable-linger agent'; if it is refused, run 'sudo loginctl enable-linger agent'" {
		t.Fatalf("linger problem=%+v", byName["linger-off"])
	}
	if byName["service-not-installed"].Fix != "run 'agenthail daemon install'" {
		t.Fatalf("service problem=%+v", byName["service-not-installed"])
	}
	if !strings.Contains(byName["claude-missing"].Fix, "AGENTHAIL_CLAUDE_BIN") {
		t.Fatalf("claude problem=%+v", byName["claude-missing"])
	}
	if _, ok := byName["codex-home-noexec"]; ok {
		t.Fatalf("exec-capable CODEX_HOME reported: %+v", problems)
	}
}

func TestSystemdProblemsReadTheDaemonEnvironmentFromTheUnit(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	fake.linger = true
	claudeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(claudeDir, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := captureStdout(t, service.Install); err != nil {
		t.Fatal(err)
	}
	if problems := service.Problems(); len(problems) != 0 {
		t.Fatalf("problems=%+v", problems)
	}
	t.Setenv("PATH", t.TempDir())
	if problems := service.Problems(); len(problems) != 0 {
		t.Fatalf("calling shell PATH replaced the unit PATH: %+v", problems)
	}
}

func TestDoctorFailsOnDaemonServiceProblems(t *testing.T) {
	fake := &cliSurface{kind: "claude"}
	app, _ := cliFixture(t, fake)
	app.daemonService = &fakeService{problems: []serviceProblem{{Problem: "linger-off", Detail: "linger is off", Fix: "run 'loginctl enable-linger agent'"}}}
	output, err := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	if err == nil || !strings.Contains(err.Error(), "1 daemon problem") {
		t.Fatalf("err=%v output=%s", err, output)
	}
	var payload struct {
		OK     bool `json:"ok"`
		Daemon struct {
			Supervisor string           `json:"supervisor"`
			Problems   []serviceProblem `json:"problems"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil || payload.OK || payload.Daemon.Supervisor != "fake" || len(payload.Daemon.Problems) != 1 || payload.Daemon.Problems[0].Fix != "run 'loginctl enable-linger agent'" {
		t.Fatalf("payload=%+v err=%v output=%s", payload, err, output)
	}
	text, err := captureStdout(t, func() error { return app.Run([]string{"doctor"}) })
	if err == nil || !strings.Contains(text, "[daemon] supervisor: fake") || !strings.Contains(text, "  fix: run 'loginctl enable-linger agent'") {
		t.Fatalf("err=%v text=%s", err, text)
	}
}

func TestDoctorReportsDaemonUnhealthyWhileLingerIsOff(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	fake.lingerRefused = true
	claudeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(claudeDir, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := captureStdout(t, service.Install); err != nil {
		t.Fatal(err)
	}
	app, _ := cliFixture(t, &cliSurface{kind: "claude"})
	app.daemonService = service
	output, err := captureStdout(t, func() error { return app.Run([]string{"doctor"}) })
	if err == nil || !strings.Contains(err.Error(), "1 daemon problem") {
		t.Fatalf("doctor accepted a daemon that stops at logout: err=%v output=%s", err, output)
	}
	if !strings.Contains(output, "linger is off for agent") || !strings.Contains(output, "run 'sudo loginctl enable-linger agent'") {
		t.Fatalf("output=%s", output)
	}
}

func TestSystemdUnitRecordsClaudeConfigDirAndDoctorFlagsDrift(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	fake.linger = true
	claudeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(claudeDir, "claude"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	configured := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configured)
	if _, err := captureStdout(t, service.Install); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(fake.unitPath)
	if err != nil {
		t.Fatal(err)
	}
	if systemdUnitEnvironment(string(unit))["CLAUDE_CONFIG_DIR"] != configured {
		t.Fatalf("unit=%s", unit)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	problems := service.Problems()
	if len(problems) != 1 || problems[0].Problem != "claude-config-dir-drift" || !strings.Contains(problems[0].Detail, "CLAUDE_CONFIG_DIR="+configured) {
		t.Fatalf("problems=%+v", problems)
	}
	app, _ := cliFixture(t, &cliSurface{kind: "claude"})
	t.Setenv("CLAUDE_CONFIG_DIR", configured)
	output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	var payload struct {
		ClaudeConfigDir string `json:"claudeConfigDir"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil || payload.ClaudeConfigDir != configured {
		t.Fatalf("payload=%+v err=%v output=%s", payload, err, output)
	}
}

func TestSystemdLifecycleRefusesUnsupervisedFallbackWhenManagerIsUnreachable(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	if _, err := captureStdout(t, service.Install); err != nil {
		t.Fatal(err)
	}
	fake.stopDaemon()
	fake.managerDown = true
	for name, operation := range map[string]func() error{"start": service.Start, "stop": service.Stop, "restart": service.Restart, "uninstall": service.Uninstall} {
		output, err := captureStdout(t, operation)
		if err == nil || !strings.Contains(err.Error(), "agenthail.service is installed but the systemd user manager is unreachable") || !strings.Contains(err.Error(), "loginctl enable-linger agent") {
			t.Fatalf("%s err=%v output=%s", name, err, output)
		}
		if _, running := daemon.IsRunning(); running {
			t.Fatalf("%s started an unsupervised daemon", name)
		}
	}
	if _, err := os.Stat(fake.unitPath); err != nil {
		t.Fatalf("unit removed while the manager was unreachable: %v", err)
	}
	if err := service.Check(); err == nil {
		t.Fatal("Check accepted an unreachable manager")
	}
}

func TestDaemonStatusShowsSystemdStateWhenDaemonIsStopped(t *testing.T) {
	fake, service := newFakeSystemctl(t)
	fake.linger = true
	if _, err := captureStdout(t, service.Install); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, service.Stop); err != nil {
		t.Fatal(err)
	}
	app := &App{daemonService: service}
	output, err := captureStdout(t, func() error { return app.Run([]string{"daemon", "status"}) })
	if err == nil || !strings.Contains(output, "service: systemd agenthail.service (inactive)") || !strings.Contains(output, "linger: on") {
		t.Fatalf("err=%v output=%s", err, output)
	}
	fake.managerDown = true
	output, _ = captureStdout(t, func() error { return app.Run([]string{"daemon", "status"}) })
	if !strings.Contains(output, "service: systemd agenthail.service (manager unreachable)") {
		t.Fatalf("output=%s", output)
	}
}

func TestSystemdUnitEscapesLogPath(t *testing.T) {
	unit, err := systemdUnit("/opt/agenthail", map[string]string{}, `/home/a\b 100%/.agenthail/daemon.log`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unit, `StandardOutput=append:/home/a\b 100%%/.agenthail/daemon.log`) {
		t.Fatalf("unit=%s", unit)
	}
	for _, bad := range []string{"/home/a\nb/daemon.log", "/home/a\rb/daemon.log"} {
		if _, err := systemdUnit("/opt/agenthail", map[string]string{}, bad); err == nil {
			t.Fatalf("log path %q accepted", bad)
		}
	}
}
