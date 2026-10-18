package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

const systemdUnitName = "agenthail.service"

type systemdService struct {
	run      func(name string, args ...string) ([]byte, error)
	unitPath string
	user     string
	uid      int
	home     string
}

func newSystemdService() *systemdService {
	home, _ := os.UserHomeDir()
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	username := os.Getenv("USER")
	if current, err := user.Current(); err == nil {
		username = current.Username
	}
	return &systemdService{
		run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		unitPath: filepath.Join(configHome, "systemd", "user", systemdUnitName),
		user:     username,
		uid:      os.Getuid(),
		home:     home,
	}
}

func (s *systemdService) Name() string { return "systemd" }

func (s *systemdService) systemctl(args ...string) (string, error) {
	output, err := s.run("systemctl", append([]string{"--user"}, args...)...)
	return strings.TrimSpace(string(output)), err
}

func (s *systemdService) managerProblem() *serviceProblem {
	output, err := s.systemctl("show", "--property=Version")
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return &serviceProblem{
			Problem: "systemd-missing",
			Detail:  "systemctl is not installed, so no service manager can supervise the daemon",
			Fix:     "run the daemon unsupervised with 'agenthail daemon start'",
		}
	}
	if output == "" {
		output = err.Error()
	}
	return &serviceProblem{
		Problem: "systemd-user-unreachable",
		Detail:  "the systemd user manager is unreachable: " + output,
		Fix:     fmt.Sprintf("run 'loginctl enable-linger %s', then reconnect so XDG_RUNTIME_DIR=/run/user/%d is set", s.user, s.uid),
	}
}

func (s *systemdService) Loaded() bool {
	if _, err := os.Stat(s.unitPath); err != nil {
		return false
	}
	state, err := s.systemctl("show", systemdUnitName, "--property=LoadState", "--value")
	return err == nil && state == "loaded"
}

func (s *systemdService) Start() error {
	if !s.Loaded() {
		return startUnsupervisedDaemon()
	}
	if pid, ok := daemon.IsRunning(); ok {
		return fmt.Errorf("daemon already running (pid %d)", pid)
	}
	if output, err := s.systemctl("start", systemdUnitName); err != nil {
		return fmt.Errorf("start %s: %w (%s)", systemdUnitName, err, output)
	}
	pid, ok := waitForDaemonPID(0, 15*time.Second)
	if !ok {
		return fmt.Errorf("systemd started %s but the daemon did not become ready; inspect %s", systemdUnitName, daemon.LogFilePath())
	}
	fmt.Printf("daemon started (pid %d) by systemd %s\n", pid, systemdUnitName)
	return nil
}

func (s *systemdService) Stop() error {
	if !s.Loaded() {
		return stopUnsupervisedDaemon()
	}
	pid, running := daemon.IsRunning()
	if output, err := s.systemctl("stop", systemdUnitName); err != nil {
		return fmt.Errorf("stop %s: %w (%s)", systemdUnitName, err, output)
	}
	if !running {
		fmt.Println("daemon not running")
		return nil
	}
	fmt.Printf("daemon stopped (was pid %d); agents it started keep running\n", pid)
	return nil
}

func (s *systemdService) Restart() error {
	if !s.Loaded() {
		return restartUnsupervisedDaemon()
	}
	before, _ := daemon.IsRunning()
	if output, err := s.systemctl("restart", systemdUnitName); err != nil {
		return fmt.Errorf("restart %s: %w (%s)", systemdUnitName, err, output)
	}
	pid, ok := waitForDaemonPID(before, 15*time.Second)
	if !ok {
		return fmt.Errorf("systemd restarted %s but the daemon did not become ready; inspect %s", systemdUnitName, daemon.LogFilePath())
	}
	fmt.Printf("daemon restarted (pid %d)\n", pid)
	return nil
}

func (s *systemdService) Install() error {
	if problem := s.managerProblem(); problem != nil {
		return fmt.Errorf("%s; %s", problem.Detail, problem.Fix)
	}
	environment := serviceEnvironment()
	environment["AGENTHAIL_DAEMON_SUPERVISOR"] = "systemd"
	if problems := storageProblems(environment, s.unitPath); len(problems) > 0 {
		return fmt.Errorf("%s; %s", problems[0].Detail, problems[0].Fix)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate agenthail binary: %w", err)
	}
	if s.Loaded() {
		if output, err := s.systemctl("stop", systemdUnitName); err != nil {
			return fmt.Errorf("stop %s: %w (%s)", systemdUnitName, err, output)
		}
	} else if _, running := daemon.IsRunning(); running {
		if err := daemon.Stop(); err != nil {
			return fmt.Errorf("stop existing daemon: %w", err)
		}
	}
	if _, err := os.Stat(daemon.DashboardConfigPath()); os.IsNotExist(err) {
		config, loadErr := daemon.LoadDashboardConfig()
		if loadErr != nil {
			return loadErr
		}
		config.Enabled = true
		if err := daemon.SaveDashboardConfig(config); err != nil {
			return err
		}
	}
	logPath := daemon.LogFilePath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return err
	}
	if err := rotateLogFile(logPath, daemonLogMaxBytes); err != nil {
		return fmt.Errorf("rotate daemon log: %w", err)
	}
	unit, err := systemdUnit(exe, environment, logPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.unitPath), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(s.unitPath, []byte(unit), 0600); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", systemdUnitName}, {"restart", systemdUnitName}} {
		if output, err := s.systemctl(args...); err != nil {
			return fmt.Errorf("systemctl --user %s: %w (%s)", strings.Join(args, " "), err, output)
		}
	}
	pid, ok := waitForDaemonPID(0, 15*time.Second)
	if !ok {
		return fmt.Errorf("systemd started %s but the daemon did not become ready; inspect %s", systemdUnitName, logPath)
	}
	fmt.Printf("installed and started systemd user service %s (pid %d)\n", systemdUnitName, pid)
	s.ensureLinger()
	return nil
}

func (s *systemdService) lingerEnabled() bool {
	output, err := s.run("loginctl", "show-user", s.user, "--property=Linger", "--value")
	return err == nil && strings.TrimSpace(string(output)) == "yes"
}

func (s *systemdService) ensureLinger() {
	if s.lingerEnabled() {
		fmt.Println("linger: on")
		return
	}
	if output, err := s.run("loginctl", "enable-linger", s.user); err != nil || !s.lingerEnabled() {
		reason := strings.TrimSpace(string(output))
		if reason == "" && err != nil {
			reason = err.Error()
		}
		fmt.Printf("linger: off (%s); run 'sudo loginctl enable-linger %s' so the daemon runs without a login session\n", reason, s.user)
		return
	}
	fmt.Println("linger: on")
}

func (s *systemdService) Uninstall() error {
	if s.Loaded() {
		if output, err := s.systemctl("disable", "--now", systemdUnitName); err != nil {
			return fmt.Errorf("disable %s: %w (%s)", systemdUnitName, err, output)
		}
	}
	if err := os.Remove(s.unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if output, err := s.systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %w (%s)", err, output)
	}
	fmt.Printf("uninstalled systemd user service %s; agents it started keep running\n", systemdUnitName)
	return nil
}

func (s *systemdService) StatusLines() []string {
	if !s.Loaded() {
		return []string{"service: not installed (run 'agenthail daemon install')"}
	}
	state, _ := s.systemctl("is-active", systemdUnitName)
	linger := "off"
	if s.lingerEnabled() {
		linger = "on"
	}
	return []string{fmt.Sprintf("service: systemd %s (%s)", systemdUnitName, state), "linger: " + linger}
}

func (s *systemdService) Problems() []serviceProblem {
	if problem := s.managerProblem(); problem != nil {
		return []serviceProblem{*problem}
	}
	var problems []serviceProblem
	if !s.lingerEnabled() {
		problems = append(problems, serviceProblem{
			Problem: "linger-off",
			Detail:  fmt.Sprintf("linger is off for %s, so systemd stops the daemon when the last login session ends", s.user),
			Fix:     fmt.Sprintf("run 'loginctl enable-linger %s'; if it is refused, run 'sudo loginctl enable-linger %s'", s.user, s.user),
		})
	}
	environment := serviceEnvironment()
	if !s.Loaded() {
		problems = append(problems, serviceProblem{
			Problem: "service-not-installed",
			Detail:  fmt.Sprintf("%s is not installed, so nothing restarts the daemon after a crash or reboot", systemdUnitName),
			Fix:     "run 'agenthail daemon install'",
		})
	} else {
		if state, _ := s.systemctl("is-active", systemdUnitName); state != "active" {
			problems = append(problems, serviceProblem{
				Problem: "service-inactive",
				Detail:  fmt.Sprintf("%s is %s", systemdUnitName, state),
				Fix:     fmt.Sprintf("run 'systemctl --user restart %s' and inspect %s", systemdUnitName, daemon.LogFilePath()),
			})
		}
		if data, err := os.ReadFile(s.unitPath); err == nil {
			environment = systemdUnitEnvironment(string(data))
		}
	}
	if _, err := claudeExecutable(environment, s.home); err != nil {
		problems = append(problems, serviceProblem{
			Problem: "claude-missing",
			Detail:  err.Error(),
			Fix:     "install Claude Code with 'curl -fsSL https://claude.ai/install.sh | bash', or set AGENTHAIL_CLAUDE_BIN, then run 'agenthail daemon install'",
		})
	}
	return append(problems, storageProblems(environment, s.unitPath)...)
}

func claudeExecutable(environment map[string]string, home string) (string, error) {
	if configured := strings.TrimSpace(environment["AGENTHAIL_CLAUDE_BIN"]); configured != "" {
		if isExecutable(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("AGENTHAIL_CLAUDE_BIN %q is not executable", configured)
	}
	for _, dir := range filepath.SplitList(environment["PATH"]) {
		if dir == "" {
			continue
		}
		if candidate := filepath.Join(dir, "claude"); isExecutable(candidate) {
			return candidate, nil
		}
	}
	installed := filepath.Join(home, ".local", "bin", "claude")
	if isExecutable(installed) {
		return installed, nil
	}
	return "", fmt.Errorf("Claude executable was not found on the daemon PATH or at %s", installed)
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

func storageProblems(environment map[string]string, unitPath string) []serviceProblem {
	var problems []serviceProblem
	stateDir := filepath.Dir(daemon.PidFilePath())
	if reason := probeExec(stateDir); reason != "" {
		problems = append(problems, serviceProblem{
			Problem: "state-dir-noexec",
			Detail:  fmt.Sprintf("%s cannot run executables (%s)", stateDir, reason),
			Fix:     fmt.Sprintf("move %s onto an exec-capable filesystem such as ext4", stateDir),
		})
	} else if reason := probeSQLite(stateDir); reason != "" {
		problems = append(problems, serviceProblem{
			Problem: "state-dir-sqlite",
			Detail:  fmt.Sprintf("%s cannot hold a SQLite database (%s)", stateDir, reason),
			Fix:     fmt.Sprintf("move %s onto a local filesystem such as ext4", stateDir),
		})
	}
	codexHome := codexHomeFrom(environment)
	if reason := probeExec(codexHome); reason != "" {
		problems = append(problems, serviceProblem{
			Problem: "codex-home-noexec",
			Detail:  fmt.Sprintf("%s cannot run executables (%s)", codexHome, reason),
			Fix:     fmt.Sprintf("point CODEX_HOME at an exec-capable path in %s.d or rerun 'agenthail daemon install' with CODEX_HOME set", unitPath),
		})
	} else if reason := probeSQLite(codexHome); reason != "" {
		problems = append(problems, serviceProblem{
			Problem: "codex-home-sqlite",
			Detail:  fmt.Sprintf("%s cannot hold a SQLite database (%s)", codexHome, reason),
			Fix:     fmt.Sprintf("point CODEX_HOME at a local filesystem path in %s.d or rerun 'agenthail daemon install' with CODEX_HOME set", unitPath),
		})
	}
	return problems
}

func probeSQLite(dir string) string {
	file, err := os.CreateTemp(dir, ".agenthail-sqlite-probe-")
	if err != nil {
		return err.Error()
	}
	path := file.Name()
	file.Close()
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			os.Remove(path + suffix)
		}
	}()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err.Error()
	}
	defer db.Close()
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE probe (value INTEGER)", "INSERT INTO probe VALUES (1)"} {
		if _, err := db.Exec(statement); err != nil {
			return err.Error()
		}
	}
	return ""
}

func systemdUnit(exe string, environment map[string]string, logPath string) (string, error) {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Agenthail daemon\nDocumentation=https://github.com/zm2231/agenthail\n\n[Service]\nType=simple\n")
	quotedExe, err := systemdQuote(exe, true)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "ExecStart=%s daemon-run\n", quotedExe)
	keys := append(append([]string(nil), serviceEnvironmentKeys...), "AGENTHAIL_DAEMON_SUPERVISOR")
	for _, key := range keys {
		if environment[key] == "" {
			continue
		}
		quoted, err := systemdQuote(key+"="+environment[key], false)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "Environment=%s\n", quoted)
	}
	if strings.ContainsAny(logPath, "\n") {
		return "", fmt.Errorf("log path %q cannot be written to a systemd unit", logPath)
	}
	escapedLog := strings.ReplaceAll(logPath, "%", "%%")
	// KillMode=process limits stop and restart to the daemon itself. tmux
	// servers and Codex app-servers the daemon launched share its cgroup, and
	// the default control-group mode would kill every agent with it.
	fmt.Fprintf(&b, "Restart=always\nRestartSec=2\nKillMode=process\nStandardOutput=append:%s\nStandardError=append:%s\n\n[Install]\nWantedBy=default.target\n", escapedLog, escapedLog)
	return b.String(), nil
}

func systemdQuote(value string, command bool) (string, error) {
	if strings.ContainsAny(value, "\n\r") {
		return "", fmt.Errorf("value %q cannot be written to a systemd unit", value)
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%")
	quoted := replacer.Replace(value)
	if command {
		quoted = strings.ReplaceAll(quoted, "$", "$$")
	}
	return `"` + quoted + `"`, nil
}

func systemdUnitEnvironment(unit string) map[string]string {
	environment := map[string]string{}
	for _, line := range strings.Split(unit, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=")
		if !ok || len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			continue
		}
		unquoted := strings.NewReplacer(`\\`, `\`, `\"`, `"`, "%%", "%").Replace(value[1 : len(value)-1])
		if key, setting, found := strings.Cut(unquoted, "="); found {
			environment[key] = setting
		}
	}
	return environment
}
