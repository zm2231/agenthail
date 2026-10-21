package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/permissions"

	"github.com/zm2231/agenthail/internal/codexconfig"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/report"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/workspace"
)

type SurfaceEntry struct {
	Name    string
	Surface surface.Surface
}

type App struct {
	// Registry is the open registry. When it is nil, the first command that
	// needs it opens one with OpenRegistry, so help, version and argument
	// errors never create or migrate a registry.
	Registry                  *registry.Registry
	OpenRegistry              func() (*registry.Registry, error)
	registryMu                sync.Mutex
	registryErr               error
	Surfaces                  []SurfaceEntry
	DefaultTimeout            time.Duration
	Version                   string
	Revision                  string
	BuiltAt                   string
	catalogDaemonRunning      func() bool
	daemonService             daemonService
	update                    *updateDeps
	daemonSessionPageReader   func() (sessionPageReader, error)
	daemonSessionStreamReader func() (sessionStreamReader, error)
	daemonAPIOverride         func() (*daemonAPIClient, error)
}

// registryUnavailable carries an open failure out of the command that needed
// the registry; Run turns it back into that command's error.
type registryUnavailable struct{ err error }

// registry returns the registry, opening it on first use. A failure to open
// ends the command with that error, since every caller needs the registry to
// go on.
func (a *App) registry() *registry.Registry {
	r, err := a.openRegistry()
	if err != nil {
		panic(registryUnavailable{err})
	}
	return r
}

func (a *App) openRegistry() (*registry.Registry, error) {
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	if a.Registry != nil || a.OpenRegistry == nil || a.registryErr != nil {
		return a.Registry, a.registryErr
	}
	a.Registry, a.registryErr = a.OpenRegistry()
	if a.registryErr != nil {
		a.registryErr = fmt.Errorf("open registry: %w", a.registryErr)
	} else {
		a.Registry.SetBusyPolicy(deliverypolicy.Load)
	}
	return a.Registry, a.registryErr
}

// ResolveSignin lets surfaces built before the registry is open read sign-in
// choices from it once they need them.
func (a *App) ResolveSignin(target signin.Target) (signin.Source, error) {
	r, err := a.openRegistry()
	if err != nil {
		return signin.Source{}, err
	}
	if r == nil {
		return signin.Source{}, errors.New("the registry is not available")
	}
	return r.ResolveSignin(target)
}

// Close closes the registry if a command opened it.
func (a *App) Close() error {
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	if a.Registry == nil {
		return nil
	}
	return a.Registry.Close()
}

func (a *App) Run(args []string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			unavailable, ok := recovered.(registryUnavailable)
			if !ok {
				panic(recovered)
			}
			err = unavailable.err
		}
	}()
	if len(args) == 0 {
		a.usage()
		return nil
	}
	cmd := args[0]
	rest := args[1:]
	if cmd == "codex" {
		return a.cmdCodex(rest)
	}
	if cmd == "launcher-exec" {
		return a.cmdLauncherExec(rest)
	}
	if err := validateCommandFlags(cmd, rest); err != nil {
		return err
	}

	switch cmd {
	case "list", "ls":
		return a.cmdList(rest)
	case "runs":
		return a.cmdClaudeRuns(rest)
	case "whoami":
		return a.cmdWhoami(rest)
	case "roots":
		return a.cmdRoots(rest)
	case "claude":
		return a.cmdClaude(rest)
	case "search":
		return a.cmdSearch(rest)
	case "send":
		return a.cmdSend(rest)
	case "reply":
		return a.cmdReply(rest)
	case "last", "tail":
		return a.cmdLast(rest)
	case "stream":
		return a.cmdStream(rest)
	case "goal":
		return a.cmdGoal(rest)
	case "compact":
		return a.cmdCompact(rest)
	case "model":
		return a.cmdModel(rest)
	case "permission-mode":
		return a.cmdPermissionMode(rest)
	case "requests":
		return a.cmdRequests(rest)
	case "answer":
		return a.cmdAnswer(rest)
	case "control":
		return a.cmdControl(rest)
	case "interrupt":
		return a.cmdInterrupt(rest)
	case "steer":
		return a.cmdSteer(rest)
	case "queue":
		return a.cmdQueue(rest)
	case "history":
		return a.cmdHistory(rest)
	case "thread":
		return a.cmdThread(rest)
	case "hosts":
		return a.cmdHosts(rest)
	case "move":
		return a.cmdMove(rest)
	case "project", "projects":
		return a.cmdProject(rest)
	case "signin":
		return a.cmdSignin(rest)
	case "identify":
		return a.cmdIdentify(rest)
	case "coordinator":
		return a.cmdCoordinator(rest)
	case "channel":
		return a.cmdChannel(rest)
	case "relay":
		return a.cmdRelay(rest)
	case "daemon":
		return a.cmdDaemon(rest)
	case "daemon-run":
		return a.daemonRun()
	case "launch":
		return a.cmdLaunch(rest)
	case "doctor":
		return a.cmdDoctor(rest)
	case "hook":
		return a.cmdHook(rest)
	case "hooks":
		return a.cmdHooks(rest)
	case "permissions":
		return a.cmdPermissions(rest)
	case "dashboard":
		return a.cmdDashboard(rest)
	case "version", "--version":
		return a.cmdVersion(rest)
	case "update", "upgrade":
		return a.cmdUpdate(rest)
	case "help", "-h", "--help":
		a.usage()
		return nil
	default:
		return fmt.Errorf("unknown command '%s' (try 'help')", cmd)
	}
}

func (a *App) usage() {
	fmt.Print(`agenthail - hail an agent

Usage:
  agenthail <command> [target] [args] [options]

Session commands:
  codex [args]                  Start a writable Codex terminal session
  codex --repair-managed-runtime Restart a stale managed Codex runtime
  thread create <codex|claude> "msg"    Create a conversation
  thread fork <target>         Fork a Codex conversation
  thread <status|stop|resume|logs> <target>  Manage a Claude session (logs: claude-bg jobs)
  thread <archive|unarchive> <target>  Hide a session until it is active again
  thread adopt <target>        Open a terminal Claude session in Agenthail
  thread queue <target> <list|add|update|delete|reorder|start>  Manage Codex native input
  list [--all|--archived] [--project <name|id>|--cwd <path>] [--wide]
                                 List current sessions (with the daemon: --all or --archived);
                                 --cwd lists the project that holds that folder
  project list|add <path>|remove <name>|archive <name>|unarchive <name>
                                 List, add or remove project folders, or hide a project
  runs [--json]                  Read verified Claude job and subagent records
  whoami [--json]                Show the caller session bound to this process
  roots [--json]                 List each Claude config directory and the login it currently holds
  search codex <query>           Search older Codex conversation history on demand
  send <target> "msg"|-       Send now when idle; queue when busy (use --no-queue to refuse delay)
  stream <target>               Tail live activity
  reply <target> [--before cursor] [--json] [--max-bytes N | --report] [--timeout 30s]  Fetch last assistant reply, capped or as its completion report
  last <target> [count] [--before cursor] [--full] [--json] [--max-bytes N | --report] [--timeout 30s]  Show a bounded exchange page
  goal <target> [set|edit|pause|resume|budget|clear] [value] [--json]
                                 Read goal usage or control its objective, status and budget
  compact <target>              Compress context (typed control; unsupported for socket-only Claude peers)
  model <target> [name] [--json] [--timeout 30s]
                                 Get or set model; Claude waits for its own control response
  permission-mode <target> [mode] [--json]
                                 Get or set the permission mode of a session the Claude host runs
  requests <target> [--json]     List the tool approvals and questions a session waits on you for
  answer <target> <allow|always|deny|"answer"> [--request id] [--reason text] [--answers json]
                                 Answer the oldest open request, or the one --request names
  interrupt <target> [--json] [--timeout 30s]
                                 Stop current turn; Claude waits for the turn to end
  steer <target> "message" [--json] [--timeout 30s]
                                 Inject guidance into a running turn; use send when idle
  control <target> <context|usage|status|effort|rename> [value] [--json] [--timeout 30s]
                                 Run a Claude Remote Control command and show its output
  queue <target> "msg"|-        Hold until target is ready; start daemon to deliver queued work
  queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>|--cwd <path>]
                                 Inspect queued work scoped to a session or project
  queue retry <id>              Retry a failed message
  queue rm <id>                 Cancel a pending queued message
  queue clear <target>          Cancel all pending messages for a target
  history [target] [count]       Show delivery history (default 50)

Identity:
  identify <target> <name>      Name a session (henceforth @name resolves to it)
  identify rm <name>             Remove an alias
  identify list                 Show all names

Coordinator (one per project; a message to it always lands):
  coordinator [--cwd <path>] [--json]  Show the coordinator of this folder's project
  coordinator list [--json]     Show every project's coordinator
  coordinator set <target> [--name <name>] [--instructions <file>] [--permission-mode <mode>]
                                 Make an existing session its project's coordinator
  coordinator new <claude|codex> [--cwd <path>] [--name <name>] [--model <model>] [--instructions <file>] [--permission-mode <mode>]
                                 Define this folder's project coordinator (default @<project>-lead); the first message starts it
  coordinator clear [--cwd <path>]  Stop treating any session as this project's coordinator
  @lead addresses the coordinator of the sender's project. A send to a coordinator
  steers it when busy, resumes it when ended, and starts a new conversation from
  its saved instructions when its conversation is gone.

Channels:
  channel create <name>         Create a channel
  channel add <name> <target>   Add a session to a channel
  channel rm <name> <target>   Remove a session from a channel (--all deletes)
  channel list                  List channels + members (shows @alias names)
  channel rename <name> <new>   Rename a channel
  channel send <name> "msg"     Broadcast to all members (--from <name>)
  channel send '#<project>' "msg"  Message a project's active conversations
                                on every host and its coordinator

Automatic handoffs:
  relay add <from> <to> [regex] [--once] [--when turn_finished|goal_completed]
  relay list [--json]           Show routing rules and derived firing evidence
  relay rm <id>                 Remove a rule

Hosts:
  hosts [--json]                 List this host and paired hosts with their health
  hosts invite                  Print a pairing link for another host's daemon
  hosts add <link> [--address <tailnet-ip>] [--name <name>]
                                 Pair a host so its sessions appear here
  hosts remove <host>            Unpair a host and drop its sessions from this host
  hosts name [name]              Show or set this host's name
  move <target> --to <host> [--wait <duration>] [--stop-terminal] [--json]
                                 Move an idle Claude session to a paired host
  move list [--unfinished] [--json]
                                 Show moves to and from this host

Browser sign-in (Notion and Claude Remote Control only):
  signin [list] [--json]        Show consumers, sign-in profiles and this computer's browser profiles
  signin add <browser> <profile> [--label <label>]
                                Add a browser profile to read sign-in cookies from
  signin use <notion|claude> <profile|none>
                                Choose the profile a consumer reads cookies from
  signin rm <profile>           Remove a sign-in profile

Background service:
  daemon start                  Start Agenthail in the background
  daemon stop                   Stop Agenthail in the background
  daemon restart                Restart Agenthail in the background
  daemon status                 Check whether Agenthail is running
  daemon notify on|off|status|test|settings
                                Native macOS completion notifications
  daemon install                Keep Agenthail running after login and restarts
  daemon uninstall              Stop Agenthail from starting automatically

Claude prompt hook (answer terminal Claude prompts from Agenthail):
  hooks install                 Add the PermissionRequest hook to every Claude config root
  hooks uninstall               Remove it
  hooks status                  Show where it is installed

Dashboard (optional):
  dashboard enable              Enable the local dashboard and open it
  dashboard disable             Disable the dashboard listener
  dashboard status              Show dashboard state and URL
  dashboard config --codex-recent-hours <hours>
                                Set the Codex current-session window
  dashboard remote              Enable private phone access with Tailscale + QR
  dashboard remote status       Show remote access state and phone URL
  dashboard remote off          Remove Agenthail's Tailscale Serve route
  dashboard                     Open an enabled dashboard

Other:
  launch <surface>              Launch a surface app with debug settings
  doctor [--json]               Health check (nonzero when any surface is unhealthy)
  permissions [--json]          The daemon's macOS privacy grants and where to change them
  permissions request           Have macOS ask for the grants the daemon lacks
  update [--check] [--json]     Install the latest Agenthail release
  upgrade [--check] [--json]    Alias for update
  version [--json]              Build and revision information

Targets: @name, PID, session id prefix, cwd/name fragment, surface:target, or host::target.
        A target that matches on more than one host is an error; qualify it as host::target.
        send, steer, interrupt, reply, last, stream and thread create --host reach paired hosts.
Sender: --from resolves a session; otherwise identity comes from AGENTHAIL_SESSION_ID,
        CLAUDE_CODE_SESSION_ID, then CODEX_THREAD_ID.
        Native Claude peers require the daemon and register automatically before sending.
`)
}

func (a *App) cmdCodex(args []string) error {
	if len(args) == 1 && args[0] == "--repair-managed-runtime" {
		return repairManagedCodexRuntime(context.Background())
	}
	for _, arg := range args {
		if arg == "--remote" || strings.HasPrefix(arg, "--remote=") || arg == "--remote-auth-token-env" || strings.HasPrefix(arg, "--remote-auth-token-env=") {
			return fmt.Errorf("agenthail codex manages the remote transport; remove %s", arg)
		}
	}
	path, err := surfaces.CodexDaemonCLI()
	if err != nil {
		return fmt.Errorf("find codex: %w", err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelStart()
	if err := surfaces.NewCodex("").EnsureRuntime(startCtx); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	if receiptPath := strings.TrimSpace(os.Getenv("AGENTHAIL_CODEX_LAUNCH_RECEIPT")); receiptPath != "" || strings.TrimSpace(os.Getenv("AGENTHAIL_CODEX_LAUNCH_ID")) != "" {
		launchID := strings.TrimSpace(os.Getenv("AGENTHAIL_CODEX_LAUNCH_ID"))
		if launchID == "" || receiptPath == "" {
			return fmt.Errorf("managed Codex launch identity is incomplete")
		}
		expectedReceiptPath, err := surface.ManagedCodexLaunchReceiptPath(launchID)
		if err != nil || filepath.Clean(receiptPath) != filepath.Clean(expectedReceiptPath) {
			return fmt.Errorf("managed Codex launch receipt path is not launch-owned")
		}
		binding, err := managedCodexLaunchBindingFromEnv()
		if err != nil {
			return err
		}
		prepareCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return runManagedCodexLaunch(prepareCtx, args, cwd, launchID, receiptPath, binding, surfaces.NewCodex("").PrepareManagedTerminalSession, func(path string, argv, env []string) error {
			return syscall.Exec(path, argv, env)
		}, path)
	}
	argv := append([]string{"codex", "--remote", "unix://"}, codexRemoteArgs(args, cwd)...)
	return syscall.Exec(path, argv, surface.AgentEnv(os.Environ()))
}

type managedCodexPreparer func(context.Context, string, string) (*surface.Session, error)
type managedCodexExec func(string, []string, []string) error
type managedCodexLaunchBinding struct {
	Runtime   string
	TmuxPane  string
	Workspace string
	Surface   string
}

func managedCodexLaunchBindingFromEnv() (managedCodexLaunchBinding, error) {
	binding := managedCodexLaunchBinding{
		Runtime:   strings.TrimSpace(os.Getenv("AGENTHAIL_CODEX_LAUNCH_RUNTIME")),
		TmuxPane:  strings.TrimSpace(os.Getenv("TMUX_PANE")),
		Workspace: strings.TrimSpace(os.Getenv("CMUX_WORKSPACE_ID")),
		Surface:   strings.TrimSpace(os.Getenv("CMUX_SURFACE_ID")),
	}
	switch binding.Runtime {
	case surface.LauncherTMUX:
		if binding.TmuxPane == "" || !strings.HasPrefix(binding.TmuxPane, "%") {
			return managedCodexLaunchBinding{}, fmt.Errorf("managed Codex tmux launch did not receive valid TMUX_PANE")
		}
		binding.Workspace, binding.Surface = "", ""
	case surface.LauncherCMUX:
		if binding.Workspace == "" || binding.Surface == "" {
			return managedCodexLaunchBinding{}, fmt.Errorf("managed Codex CMUX launch received incomplete identity")
		}
		binding.TmuxPane = ""
	default:
		return managedCodexLaunchBinding{}, fmt.Errorf("managed Codex launch runtime is missing or unsupported")
	}
	return binding, nil
}

func runManagedCodexLaunch(ctx context.Context, args []string, cwd, launchID, receiptPath string, binding managedCodexLaunchBinding, prepare managedCodexPreparer, execute managedCodexExec, path string) error {
	model, message, err := managedCodexLaunchArgs(args)
	if err != nil {
		return err
	}
	session, err := prepare(ctx, cwd, model)
	if err != nil {
		return err
	}
	if session == nil || session.ID == "" {
		return errors.New("managed Codex launch returned no provider thread ID")
	}
	receipt := surface.ManagedCodexLaunchReceipt{LaunchID: launchID, ThreadID: session.ID, Cwd: cwd, Runtime: binding.Runtime, TmuxSession: launchID, TmuxPane: binding.TmuxPane, Workspace: binding.Workspace, Surface: binding.Surface}
	if binding.Runtime == surface.LauncherCMUX {
		receipt.TmuxSession = ""
	}
	if err := surface.WriteManagedCodexLaunchReceipt(receiptPath, receipt); err != nil {
		return err
	}
	argv := []string{"codex", "resume", session.ID, "--remote", "unix://"}
	if message != "" {
		argv = append(argv, "--", message)
	}
	return execute(path, argv, surface.AgentEnv(os.Environ()))
}

func managedCodexLaunchArgs(args []string) (string, string, error) {
	model := ""
	message := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			if index+1 >= len(args) {
				return "", "", fmt.Errorf("managed Codex launch requires a message")
			}
			if len(args) != index+2 {
				return "", "", fmt.Errorf("managed Codex launch accepts one message")
			}
			message = args[index+1]
			break
		}
		switch arg {
		case "--cd", "-C":
			if index+1 >= len(args) {
				return "", "", fmt.Errorf("managed Codex launch %s requires a directory", arg)
			}
			index++
		case "--model", "-m":
			if index+1 >= len(args) {
				return "", "", fmt.Errorf("managed Codex launch %s requires a model", arg)
			}
			model = args[index+1]
			index++
		default:
			return "", "", fmt.Errorf("managed Codex launch does not support argument %q", arg)
		}
	}
	if message == "" {
		return "", "", fmt.Errorf("managed Codex launch requires a message")
	}
	return model, message, nil
}

func repairManagedCodexRuntime(ctx context.Context) error {
	if err := surfaces.RestartManagedCodexRuntime(ctx); err != nil {
		return fmt.Errorf("restart managed Codex runtime: %w", err)
	}
	fmt.Println("Codex's app-server daemon restarted; retry the affected conversation")
	return nil
}

func codexRemoteArgs(args []string, cwd string) []string {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-C" || arg == "--cd" || strings.HasPrefix(arg, "--cd=") || strings.HasPrefix(arg, "-C=") || (strings.HasPrefix(arg, "-C") && len(arg) > 2) {
			return args
		}
	}
	return append([]string{"--cd", cwd}, args...)
}

func (a *App) cmdVersion(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail version [--json]")
	}
	result := map[string]any{"version": "dev", "revision": "unknown", "modified": false, "schemaVersion": registry.SchemaVersion()}
	if a.Version != "" {
		result["version"] = a.Version
	}
	if a.Revision != "" {
		result["revision"] = a.Revision
	}
	if a.BuiltAt != "" {
		result["builtAt"] = a.BuiltAt
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if result["version"] == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			result["version"] = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if result["revision"] == "unknown" {
					result["revision"] = setting.Value
				}
			case "vcs.time":
				if _, set := result["builtAt"]; !set {
					result["builtAt"] = setting.Value
				}
			case "vcs.modified":
				result["modified"] = setting.Value == "true"
			}
		}
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	fmt.Printf("agenthail %s (%s", result["version"], result["revision"])
	if result["modified"] == true {
		fmt.Print(", modified")
	}
	fmt.Println(")")
	return nil
}

func (a *App) allSurfaces() []surface.Surface {
	out := make([]surface.Surface, len(a.Surfaces))
	for i, e := range a.Surfaces {
		out[i] = e.Surface
	}
	return out
}

func (a *App) adapters() surface.Adapters { return surface.Adapters(a.allSurfaces()) }

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == flag {
			return true
		}
	}
	return false
}

func flagVal(args []string, flag string) string {
	for i, a := range args {
		if a == "--" {
			return ""
		}
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func stripFlags(args []string) []string {
	valueFlags := map[string]bool{"--request": true, "--reason": true, "--answers": true, "--when": true, "--effort": true, "--mode": true, "--service-tier": true, "--output-schema": true, "--cwd": true, "--target": true, "--alias": true, "--before": true, "--last-turn": true, "--id": true, "--ids": true, "--client-id": true, "--cursor": true, "--from": true, "--model": true, "--timeout": true, "--max-bytes": true, "--codex-recent-hours": true, "--tailscale": true, "--address": true, "--name": true, "--host": true, "--label": true, "--project": true, "--instructions": true, "--permission-mode": true, "--wait": true, "--root": true, "--enabled": true, "--idle": true, "--to": true}
	var out []string
	positionalOnly := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionalOnly = true
			continue
		}
		if positionalOnly {
			out = append(out, a)
			continue
		}
		if strings.HasPrefix(a, "--") {
			if valueFlags[a] && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func validateCommandFlags(command string, args []string) error {
	type flagSpec struct {
		values map[string]bool
		bools  map[string]bool
	}
	specs := map[string]flagSpec{
		"list": {values: map[string]bool{"--cwd": true, "--project": true}, bools: map[string]bool{"--all": true, "--archived": true, "--wide": true, "--json": true}}, "ls": {values: map[string]bool{"--cwd": true, "--project": true}, bools: map[string]bool{"--all": true, "--archived": true, "--wide": true, "--json": true}}, "runs": {bools: map[string]bool{"--json": true}}, "whoami": {bools: map[string]bool{"--json": true}}, "roots": {bools: map[string]bool{"--json": true}}, "claude": {values: map[string]bool{"--root": true, "--model": true, "--enabled": true, "--idle": true}, bools: map[string]bool{"--json": true}}, "search": {bools: map[string]bool{"--json": true}},
		"send":  {values: map[string]bool{"--from": true, "--model": true, "--timeout": true, "--effort": true, "--mode": true, "--service-tier": true, "--output-schema": true}, bools: map[string]bool{"--stream": true, "--reply": true, "--json": true, "--no-queue": true}},
		"reply": {values: map[string]bool{"--before": true, "--timeout": true, "--max-bytes": true}, bools: map[string]bool{"--json": true, "--report": true}}, "last": {values: map[string]bool{"--before": true, "--timeout": true, "--max-bytes": true}, bools: map[string]bool{"--full": true, "--json": true, "--report": true}}, "tail": {values: map[string]bool{"--before": true, "--timeout": true, "--max-bytes": true}, bools: map[string]bool{"--full": true, "--json": true, "--report": true}},
		"goal": {bools: map[string]bool{"--json": true}}, "queue": {}, "history": {bools: map[string]bool{"--json": true}},
		"thread":  {values: map[string]bool{"--message": true, "--cwd": true, "--alias": true, "--model": true, "--approval": true, "--timeout": true, "--effort": true, "--mode": true, "--service-tier": true, "--output-schema": true, "--name": true, "--worktree": true, "--agent": true, "--permission-mode": true, "--root": true, "--host": true, "--last-turn": true, "--id": true, "--ids": true, "--client-id": true, "--cursor": true, "--wait": true}, bools: map[string]bool{"--json": true, "--help": true, "--stop-terminal": true}},
		"channel": {},
		"project": {values: map[string]bool{"--host": true, "--name": true}, bools: map[string]bool{"--all": true, "--json": true, "--help": true}}, "projects": {values: map[string]bool{"--host": true, "--name": true}, bools: map[string]bool{"--all": true, "--json": true, "--help": true}},
		"hosts":  {values: map[string]bool{"--address": true, "--name": true}, bools: map[string]bool{"--json": true, "--help": true}},
		"move":   {values: map[string]bool{"--to": true, "--wait": true, "--timeout": true}, bools: map[string]bool{"--json": true, "--help": true, "--stop-terminal": true, "--unfinished": true}},
		"signin": {values: map[string]bool{"--label": true}, bools: map[string]bool{"--json": true, "--help": true}},
		"doctor": {bools: map[string]bool{"--json": true}}, "permissions": {bools: map[string]bool{"--json": true}}, "version": {bools: map[string]bool{"--json": true}}, "--version": {bools: map[string]bool{"--json": true}},
		"update": {bools: map[string]bool{"--check": true, "--json": true, "--help": true}}, "upgrade": {bools: map[string]bool{"--check": true, "--json": true, "--help": true}},
		"stream": {values: map[string]bool{"--timeout": true}}, "compact": {}, "model": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}},
		"permission-mode": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "requests": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}},
		"answer": {values: map[string]bool{"--timeout": true, "--request": true, "--reason": true, "--answers": true}, bools: map[string]bool{"--json": true}}, "interrupt": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "steer": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "control": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "identify": {}, "coordinator": {values: map[string]bool{"--name": true, "--instructions": true, "--permission-mode": true, "--cwd": true, "--model": true, "--host": true}, bools: map[string]bool{"--json": true, "--replace": true}}, "relay": {}, "daemon": {}, "daemon-run": {}, "hook": {}, "hooks": {}, "launch": {}, "dashboard": {values: map[string]bool{"--codex-recent-hours": true, "--tailscale": true}, bools: map[string]bool{"--no-open": true, "--json": true}}, "help": {}, "-h": {}, "--help": {},
	}
	spec, known := specs[command]
	if !known {
		return nil
	}
	if command == "queue" && len(args) > 0 && args[0] == "list" {
		spec.values = map[string]bool{"--target": true, "--cwd": true, "--project": true}
		spec.bools = map[string]bool{"--all": true, "--mine": true, "--json": true, "--help": true}
	}
	if command == "relay" && len(args) > 0 && args[0] == "add" {
		spec.values = map[string]bool{"--when": true}
		spec.bools = map[string]bool{"--once": true}
	}
	if command == "relay" && len(args) > 0 && args[0] == "list" {
		spec.bools = map[string]bool{"--json": true}
	}
	if command == "channel" && len(args) > 0 {
		switch args[0] {
		case "send", "broadcast":
			spec.values = map[string]bool{"--from": true}
		case "rm", "remove":
			spec.bools = map[string]bool{"--all": true}
		}
	}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		if seen[arg] {
			return fmt.Errorf("flag %s may only be specified once", arg)
		}
		seen[arg] = true
		if spec.values[arg] {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			continue
		}
		if spec.bools[arg] {
			continue
		}
		return fmt.Errorf("unknown flag %s for %s", arg, command)
	}
	return nil
}

func (a *App) cmdClaudeRuns(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail runs [--json]")
	}
	adapter := a.adapters().ForSurface(surface.KindClaude)
	observer, ok := adapter.(surface.ClaudeRunObserver)
	if !ok {
		return fmt.Errorf("Claude run observation is unavailable")
	}
	runs, err := observer.ObserveClaudeRuns(context.Background())
	if err != nil {
		return fmt.Errorf("read Claude job records: %w", err)
	}
	links, err := observer.ObserveAllClaudeSubagents(context.Background())
	if err != nil {
		return fmt.Errorf("read Claude subagent records: %w", err)
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"runs": runs, "subagents": links})
	}
	for _, run := range runs {
		fmt.Printf("%s\t%s\t%s\t%s\n", run.JobID, run.ProviderState, run.SessionID, run.RunType)
	}
	for _, link := range links {
		fmt.Printf("subagent\t%s\t%s\t%s\n", link.ParentSessionID, link.AgentID, link.TranscriptPath)
	}
	return nil
}

func (a *App) cmdList(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail list [--all|--archived] [--project <name|id>|--cwd <path>] [--wide] [--json]")
	}
	scope, err := listScopeFromArgs(args)
	if err != nil {
		return err
	}
	projectSelector := strings.TrimSpace(flagVal(args, "--project"))
	jsonOut := hasFlag(args, "--json")
	rootCwd := flagVal(args, "--cwd")
	if rootCwd != "" && projectSelector != "" {
		return fmt.Errorf("choose either --cwd or --project")
	}
	ctx := context.Background()

	allSessions := make([]surface.Session, 0)
	surfaceErrors := map[string]string{}
	catalog, catalogBacked, catalogErr := a.listCatalogSnapshot()
	if catalogErr != nil {
		if jsonOut {
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"sessions": allSessions, "errors": map[string]string{"catalog": catalogErr.Error()}}); err != nil {
				return err
			}
		}
		return catalogErr
	}
	successfulSurfaces := 0
	remoteRows := map[string]listHostRow{}
	views := map[string]listCatalogView{}
	if !catalogBacked && (projectSelector != "" || rootCwd != "" || hasFlag(args, "--archived")) {
		return fmt.Errorf("--project, --cwd and --archived need the daemon; start it with: agenthail daemon start")
	}
	if catalogBacked {
		successfulSurfaces = 1
		for _, record := range catalog.Sessions {
			if record.HostID != "" {
				session, row := listRemoteSession(record)
				session.Subagents = catalogSubagentRollup(record)
				remoteRows[session.ID] = row
				views[session.ID] = catalogListView(record)
				allSessions = append(allSessions, session)
				continue
			}
			views[record.Session.ID] = catalogListView(record)
			session, err := normalizeSessionCwd(record.Session)
			if err != nil {
				surfaceErrors[string(record.Session.Surface)] = fmt.Sprintf("normalize session workspace: %s", err)
				continue
			}
			session.Subagents = catalogSubagentRollup(record)
			allSessions = append(allSessions, session)
		}
		for _, record := range catalog.Surfaces {
			if record.Health != "healthy" {
				detail := record.Detail
				if detail == "" {
					detail = "catalog health is " + record.Health
				}
				surfaceErrors[string(record.Surface)] = detail
			}
		}
	} else {
		for _, s := range a.allSurfaces() {
			sessions, err := s.List(ctx)
			if err != nil {
				surfaceErrors[string(s.Name())] = err.Error()
				continue
			}
			successfulSurfaces++
			for _, sess := range sessions {
				var normalizeErr error
				sess, normalizeErr = normalizeSessionCwd(sess)
				if normalizeErr != nil {
					surfaceErrors[string(s.Name())] = fmt.Sprintf("normalize session workspace: %s", normalizeErr)
					continue
				}
				if a.registry() != nil {
					if err := a.registry().RegisterSession(sess); err != nil {
						surfaceErrors[string(s.Name())] = fmt.Sprintf("register session: %s", err)
						continue
					}
				}
				allSessions = append(allSessions, sess)
			}
		}
	}

	aliased := make(map[string]string)
	if a.registry() != nil {
		rows, _ := a.registry().ListAliases()
		for _, r := range rows {
			aliased[r.SessionID] = r.Name
		}
	}
	for id, row := range remoteRows {
		if row.Alias != "" {
			aliased[id] = row.Alias
		}
	}
	var localHost *hostRef
	pairedHosts := 0
	if a.registry() != nil {
		identity, err := a.registry().HostIdentity()
		if err != nil {
			return fmt.Errorf("read host identity: %w", err)
		}
		localHost = &hostRef{ID: identity.ID, Name: identity.Name, State: "local"}
		paired, err := a.registry().ListHosts()
		if err != nil {
			return fmt.Errorf("list paired hosts: %w", err)
		}
		pairedHosts = len(paired)
	}

	showAll := hasFlag(args, "--all")
	if showAll && a.registry() != nil {
		stored, err := a.registry().ListSessions(0)
		if err != nil {
			return fmt.Errorf("list saved sessions: %w", err)
		}
		seen := make(map[string]bool, len(allSessions))
		for _, session := range allSessions {
			seen[session.ID] = true
		}
		for _, session := range stored {
			var normalizeErr error
			session, normalizeErr = normalizeSessionCwd(session)
			if normalizeErr != nil {
				return fmt.Errorf("normalize saved session workspace: %w", normalizeErr)
			}
			if !seen[session.ID] {
				allSessions = append(allSessions, session)
			}
		}
	}
	if !showAll && !catalogBacked {
		cutoff := time.Now().AddDate(0, 0, -7)
		filtered := allSessions[:0]
		for _, s := range allSessions {
			if s.Surface == surface.KindNotion && !s.LastActive.IsZero() && s.LastActive.Before(cutoff) {
				continue
			}
			filtered = append(filtered, s)
		}
		allSessions = filtered
	}

	sort.SliceStable(allSessions, func(i, j int) bool {
		if allSessions[i].LastActive.IsZero() {
			return false
		}
		if allSessions[j].LastActive.IsZero() {
			return true
		}
		return allSessions[i].LastActive.After(allSessions[j].LastActive)
	})
	families := 15
	if showAll || catalogBacked {
		families = -1
	}
	if catalogBacked {
		projectID, err := a.selectedProject(ctx, views, projectSelector, rootCwd)
		if err != nil {
			return err
		}
		allSessions = filterListScope(allSessions, views, scope, projectID)
	}
	rows := listFamilies(allSessions, families)
	rollups := listRollups(allSessions)
	allSessions = allSessions[:0]
	for _, row := range rows {
		session := row.session
		if rollup, found := rollups[session.ID]; found {
			session.Subagents = &rollup
		}
		allSessions = append(allSessions, session)
	}
	if jsonOut {
		rowsOut := make([]listSessionView, 0, len(allSessions))
		for _, session := range allSessions {
			view := listSessionView{Session: session, Host: localHost, listCatalogView: views[session.ID]}
			if row, remote := remoteRows[session.ID]; remote {
				host := row.Host
				view.Host, view.UnavailableReason = &host, row.Unavailable
			}
			rowsOut = append(rowsOut, view)
		}
		document := map[string]any{"sessions": rowsOut, "errors": surfaceErrors}
		if catalogBacked {
			document["catalog"] = listCatalogMetadata(catalog)
			document["scope"] = scope
		}
		if err := json.NewEncoder(os.Stdout).Encode(document); err != nil {
			return err
		}
		if successfulSurfaces == 0 && len(surfaceErrors) > 0 {
			return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
		}
		return nil
	}
	for name, message := range surfaceErrors {
		if catalogBacked {
			fmt.Fprintf(os.Stderr, "warning: %s catalog health: %s\n", name, message)
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s discovery failed: %s\n", name, message)
		}
	}
	if len(allSessions) == 0 {
		fmt.Println("no sessions found")
		if successfulSurfaces == 0 && len(surfaceErrors) > 0 {
			return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
		}
		return nil
	}

	wide := hasFlag(args, "--wide")
	projectHeading := "PROJECT"
	projectWidth := 20
	if wide || !catalogBacked {
		projectHeading = "CWD"
		projectWidth = 48
	}
	showHost := pairedHosts > 0
	if showHost {
		fmt.Printf("%-12s ", "HOST")
	}
	var configRoots []surface.ClaudeConfigRoot
	rootWidth := 0
	if wide {
		rootWidth = 20
		if home, err := os.UserHomeDir(); err == nil {
			configRoots, _ = surface.DescribeClaudeConfigRoots(home)
		}
	}
	fmt.Printf("%-7s %-5s %-14s %-28s %-*s %s\n", "SURFACE", "STAT", "AGENT", "SESSION", projectWidth, projectHeading, listRootCell(wide, rootWidth, "CONFIG")+"LAST")
	if showHost {
		fmt.Printf("%-12s ", "------------")
	}
	fmt.Printf("%-7s %-5s %-14s %-28s %-*s %s\n", "-------", "-----", "--------------", "----------------------------", projectWidth, strings.Repeat("-", len(projectHeading)), listRootCell(wide, rootWidth, strings.Repeat("-", rootWidth))+"----------")
	queueCounts := map[string]int{}
	if a.registry() != nil {
		var err error
		queueCounts, err = a.registry().QueueCounts()
		if err != nil {
			return fmt.Errorf("read queue counts: %w", err)
		}
	}
	for index, s := range allSessions {
		row, remote := remoteRows[s.ID]
		queued := queueCounts[s.ID]
		if remote {
			queued = row.QueueCount
		}
		stat := sessStat(s, queued)
		if remote && row.Unavailable != "" {
			stat = "down"
		}
		if showHost {
			hostName := localHost.Name
			if remote {
				hostName = row.Host.Name
			}
			fmt.Printf("%-12s ", truncate(hostName, 12))
		}
		agent := ""
		if alias, ok := aliased[s.ID]; ok {
			agent = "@" + alias
		}
		project := listProjectColumn(views[s.ID])
		if wide || !catalogBacked {
			project = s.Cwd
		}
		last := relTime(s.LastActive)
		name := listRowName(rows[index])
		if rows[index].depth > 0 {
			name = strings.Repeat("  ", rows[index].depth-1) + "└ " + name
		}
		if s.Subagents != nil {
			last += fmt.Sprintf("  %d subagents, %d working", s.Subagents.Count, s.Subagents.Working)
		}
		configRoot := ""
		if wide && s.Surface == surface.KindClaude {
			if view := views[s.ID].ClaudeConfigRoot; view != nil {
				configRoot = view.Name
			} else if !remote {
				if derived := surface.ClaudeSessionConfigRoot(configRoots, s.Transcript); derived != nil {
					configRoot = derived.Name
				}
			}
		}
		fmt.Printf("%-7s %-5s %-14s %-28s %-*s %s\n",
			s.Surface, stat, truncate(agent, 14), truncate(name, 28), projectWidth, project, listRootCell(wide, rootWidth, truncate(configRoot, rootWidth))+last)
	}
	if successfulSurfaces == 0 && len(surfaceErrors) > 0 {
		return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
	}
	return nil
}

func listRootCell(wide bool, width int, value string) string {
	if !wide {
		return ""
	}
	return fmt.Sprintf("%-*s ", width, value)
}

type listCatalogSurface struct {
	Surface    string    `json:"surface"`
	Health     string    `json:"health"`
	Detail     string    `json:"detail,omitempty"`
	ObservedAt time.Time `json:"observedAt"`
}

type listCatalogMetadataView struct {
	Source     string                          `json:"source"`
	HostEpoch  string                          `json:"hostEpoch"`
	CatalogSeq uint64                          `json:"catalogSeq"`
	Surfaces   []listCatalogSurface            `json:"surfaces"`
	Freshness  map[string]listCatalogFreshness `json:"freshness"`
}

type listCatalogFreshness struct {
	Generation        uint64    `json:"generation"`
	ObservedAt        time.Time `json:"observedAt"`
	Stale             bool      `json:"stale"`
	UnavailableReason string    `json:"unavailableReason,omitempty"`
}

func (a *App) listCatalogSnapshot() (registry.CatalogSnapshot, bool, error) {
	if a.registry() == nil || !a.daemonIsRunning() {
		return registry.CatalogSnapshot{}, false, nil
	}
	snapshot, err := a.registry().CatalogSnapshot()
	if err != nil {
		return registry.CatalogSnapshot{}, true, fmt.Errorf("read daemon catalog: %w", err)
	}
	return snapshot, true, nil
}

func (a *App) daemonIsRunning() bool {
	if a.catalogDaemonRunning != nil {
		return a.catalogDaemonRunning()
	}
	_, running := daemon.IsRunning()
	return running
}

func listCatalogMetadata(snapshot registry.CatalogSnapshot) listCatalogMetadataView {
	metadata := listCatalogMetadataView{
		Source:     "daemon",
		HostEpoch:  snapshot.HostEpoch,
		CatalogSeq: snapshot.CatalogSeq,
		Surfaces:   make([]listCatalogSurface, 0, len(snapshot.Surfaces)),
		Freshness:  make(map[string]listCatalogFreshness, len(snapshot.Sessions)),
	}
	for _, record := range snapshot.Surfaces {
		metadata.Surfaces = append(metadata.Surfaces, listCatalogSurface{Surface: string(record.Surface), Health: record.Health, Detail: record.Detail, ObservedAt: record.ObservedAt})
	}
	for _, record := range snapshot.Sessions {
		metadata.Freshness[hosts.Qualify(record.HostID, record.Session.ID)] = listCatalogFreshness{Generation: record.Freshness.Generation, ObservedAt: record.Freshness.ObservedAt, Stale: record.Freshness.Stale, UnavailableReason: record.UnavailableReason}
	}
	return metadata
}

func normalizeSessionCwd(session surface.Session) (surface.Session, error) {
	if session.Cwd == "" {
		return session, nil
	}
	normalized, err := workspace.NormalizeCWD(session.Cwd)
	if err != nil {
		return session, err
	}
	session.Cwd = normalized
	return session, nil
}

func (a *App) cmdSearch(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 || positional[0] != "codex" {
		return fmt.Errorf("usage: agenthail search codex <query> [--json]")
	}
	adapter := a.adapters().ForSurface(surface.KindCodex)
	if adapter == nil {
		return fmt.Errorf("Codex is not configured")
	}
	searcher, ok := adapter.(surface.SessionSearcher)
	if !ok {
		return fmt.Errorf("Codex history search is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	query := strings.Join(positional[1:], " ")
	if len([]rune(strings.TrimSpace(query))) < 3 {
		return fmt.Errorf("Codex history search requires at least 3 characters")
	}
	results := make([]surface.SessionSearchResult, 0)
	if a.registry() != nil {
		stored, err := a.registry().SearchSessions(surface.KindCodex, query, 20)
		if err != nil {
			return fmt.Errorf("search saved Codex conversations: %w", err)
		}
		for _, session := range stored {
			results = append(results, surface.SessionSearchResult{Session: session, Snippet: "Saved by Agenthail"})
		}
	}
	remote, err := searcher.SearchSessions(ctx, query, 20)
	remoteError := ""
	if err != nil {
		if errors.Is(err, surface.ErrUnsupported) {
			remoteError = "Codex history search is unavailable in this Codex version"
		} else {
			remoteError = err.Error()
		}
	} else {
		results = mergeSearchResults(results, remote)
	}
	for _, result := range results {
		if a.registry() != nil {
			if err := a.registry().RegisterSession(result.Session); err != nil {
				return fmt.Errorf("register Codex search result: %w", err)
			}
		}
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"results": results, "remoteError": remoteError})
	}
	if remoteError != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", remoteError)
	}
	if len(results) == 0 {
		fmt.Println("no Codex conversations matched")
		return nil
	}
	for _, result := range results {
		fmt.Printf("%s  %s  %s\n", result.Session.ID, result.Session.Name, result.Snippet)
	}
	return nil
}

func mergeSearchResults(left, right []surface.SessionSearchResult) []surface.SessionSearchResult {
	byID := make(map[string]surface.SessionSearchResult, len(left)+len(right))
	for _, result := range left {
		byID[result.Session.ID] = result
	}
	for _, result := range right {
		byID[result.Session.ID] = result
	}
	output := make([]surface.SessionSearchResult, 0, len(byID))
	for _, result := range byID {
		output = append(output, result)
	}
	sort.Slice(output, func(i, j int) bool { return output[i].Session.LastActive.After(output[j].Session.LastActive) })
	return output
}

func sessStat(s surface.Session, queued int) string {
	busy := s.Status == surface.StatusBusy || s.Status == "running" || s.Status == "active"
	if busy && s.WaitingOn != nil {
		if queued > 0 {
			return "wait+q"
		}
		return "wait"
	}
	if busy {
		if queued > 0 {
			return "run+q"
		}
		return "run"
	}
	if surface.BackgroundRunning(s.BackgroundTasks) {
		if queued > 0 {
			return "bg+q"
		}
		return "bg"
	}
	if queued > 0 {
		return "queue"
	}
	if s.Status == surface.StatusOffline {
		return "off"
	}
	if s.Status == "idle" || s.Status == "shell" || s.Status == "notLoaded" || s.Status == surface.StatusIdle {
		return "idle"
	}
	if s.Status == "" || s.Status == surface.StatusUnknown {
		return "?"
	}
	return "idle"
}

// queuedTime parses the UTC timestamp SQLite writes for queued_at.
func queuedTime(value string) time.Time {
	t, err := time.ParseInLocation(time.DateTime, value, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

// queueStatusLabel names the row's status, with its delivery evidence beside
// it when the evidence says more than the status. A canceled row's evidence
// only records how far it got before the cancel. A delivered row to a paired
// host's session carries the host's own receipt, so "delivered (remote
// queued)" means the host accepted it into its queue.
func queueStatusLabel(row registry.QueueRow, remote bool) string {
	if row.Evidence == "" || row.Status == "canceled" || row.Evidence == registry.QueueStatusEvidence(row.Status) {
		return row.Status
	}
	evidence := string(row.Evidence)
	evidence = strings.ReplaceAll(evidence, "_", " ")
	if remote && row.Status == "delivered" {
		evidence = "remote " + evidence
	}
	return row.Status + " (" + evidence + ")"
}

func queueHoldLabel(hold registry.QueueHold) string {
	switch hold {
	case registry.QueueHeldWaiting:
		return "held: target is waiting on you"
	case registry.QueueHeldBusy:
		return "held: target is busy"
	case registry.QueueHeldOffline:
		return "held: target is offline"
	case registry.QueueHeldUnknown:
		return "held: target state is unknown"
	case registry.QueueHeldRefused:
		return "held: target refused to start; see the last error"
	case registry.QueueHeldRetrying:
		return "held: waiting to retry"
	case registry.QueueHeldBehind:
		return "held: behind an earlier message"
	case registry.QueueHeldUnloaded:
		return "held: conversation is not loaded"
	case registry.QueueHeldHostUnreachable:
		return "held: host is unreachable"
	}
	return "delivers on the next daemon scan"
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// resolveSession refreshes a session through its surface, resolving selector
// there. It is the one place a target falls back to the registry. An
// unreachable runtime is not a missing session: a session the registry already
// knows stands as registered, and each operation decides whether it can
// proceed. A session the surface no longer lists, such as a Claude session
// whose process exited, stands as registered only when the user's target names
// it exactly, so a send can still reach it; a name or folder fragment never
// resolves to a historical session. target is the user's text, which differs
// from selector when the registry already resolved it to an ID. live reports
// whether the surface gave the session; a registered row is not an
// observation, so callers register only live sessions.
func (a *App) resolveSession(ctx context.Context, kind surface.SurfaceKind, adapter surface.Surface, selector, target string) (session *surface.Session, live bool, err error) {
	session, err = adapter.Resolve(ctx, selector)
	if err == nil || a.registry() == nil {
		return session, err == nil, err
	}
	if surface.IsDeliveryUnavailable(err) {
		sid, lookupErr := a.registry().ResolveSurfaceTarget(kind, selector)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return nil, false, err
		}
		if lookupErr != nil {
			return nil, false, lookupErr
		}
		registered, lookupErr := a.registry().Session(sid)
		return registered, false, lookupErr
	}
	if !errors.Is(err, surface.ErrSessionNotFound) {
		return nil, false, err
	}
	sid, exactErr := a.registry().ExactTarget(kind, target)
	if exactErr != nil || (selector != target && sid != selector) {
		return nil, false, a.exitedNameError(kind, target, err)
	}
	registered, lookupErr := a.registry().Session(sid)
	if lookupErr != nil || registered.Surface != kind {
		return nil, false, err
	}
	return registered, false, nil
}

// exitedNamedSession refuses a target that is exactly the name of sessions the
// surface no longer lists. A name never resumes a session, so the refusal
// gives each one's id and alias to address it by.
type exitedNamedSession struct {
	kind     surface.SurfaceKind
	target   string
	sessions []registry.NamedSession
	cause    error
}

func (e exitedNamedSession) Error() string {
	handles := make([]string, 0, len(e.sessions))
	for _, session := range e.sessions {
		handle := session.ID
		if session.Alias != "" {
			handle += " (@" + session.Alias + ")"
		}
		handles = append(handles, handle)
	}
	return fmt.Sprintf("%q is the name of an exited %s session, and a name does not resume one; address it by id or alias: %s", e.target, e.kind, strings.Join(handles, ", "))
}

func (e exitedNamedSession) Unwrap() error { return e.cause }

func (a *App) exitedNameError(kind surface.SurfaceKind, target string, cause error) error {
	sessions, err := a.registry().SessionsNamed(kind, strings.TrimPrefix(target, "@"))
	if err != nil || len(sessions) == 0 {
		return cause
	}
	return exitedNamedSession{kind: kind, target: target, sessions: sessions, cause: cause}
}

func (a *App) resolveLocalTarget(ctx context.Context, target string) (*surface.Session, surface.Surface, error) {
	if a.registry() != nil {
		if registered, err := a.registry().Session(target); err == nil {
			if err := surface.NoReplyRoute(registered); err != nil {
				return nil, nil, err
			}
		}
	}
	if kindText, selector, ok := strings.Cut(target, ":"); ok {
		kind := surface.SurfaceKind(strings.ToLower(kindText))
		adapter := a.adapters().ForSurface(kind)
		if adapter == nil {
			return nil, nil, fmt.Errorf("unknown surface %q", kindText)
		}
		session, live, err := a.resolveSession(ctx, kind, adapter, selector, selector)
		if err != nil {
			return nil, nil, fmt.Errorf("%s target: %w", kind, err)
		}
		isSyntheticNotion := kind == surface.KindNotion && (session.ID == "new" || strings.HasPrefix(session.ID, "new:"))
		if a.registry() != nil && live && !isSyntheticNotion {
			if err := a.registry().RegisterSession(*session); err != nil {
				return nil, nil, fmt.Errorf("register %s session: %w", kind, err)
			}
		}
		return session, adapter, nil
	}
	subagentPath := strings.HasPrefix(target, "@") && strings.Contains(target, "/")
	named := strings.HasPrefix(target, "@") && !subagentPath && a.registry() != nil
	if !subagentPath {
		target = strings.TrimPrefix(target, "@")
	}
	if subagentPath && a.registry() == nil {
		return nil, nil, fmt.Errorf("subagent target %q needs the session registry", target)
	}
	if a.registry() != nil {
		resolve := a.registry().ResolveTarget
		if named {
			resolve = func(name string) (string, error) { return a.registry().ExactTarget("", name) }
		}
		sid, err := resolve(target)
		if err != nil && subagentPath {
			return nil, nil, err
		}
		if err == nil {
			registered, lookupErr := a.registry().Session(sid)
			if lookupErr == nil {
				adapter, adapterErr := a.adapters().For(registered)
				if adapterErr != nil {
					return nil, nil, adapterErr
				}
				session, live, resolveErr := a.resolveSession(ctx, registered.Surface, adapter, sid, target)
				var exitedName exitedNamedSession
				if errors.As(resolveErr, &exitedName) {
					return nil, nil, resolveErr
				}
				if resolveErr != nil {
					return nil, nil, fmt.Errorf("resolve registered %s session: %w", adapter.Name(), resolveErr)
				}
				if live {
					if err := a.registry().RegisterSession(*session); err != nil {
						return nil, nil, fmt.Errorf("register %s session: %w", adapter.Name(), err)
					}
				}
				return session, adapter, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
	}
	if named {
		return nil, nil, targetNotFoundError{target: "@" + target}
	}
	type resolvedMatch struct {
		session *surface.Session
		adapter surface.Surface
	}
	var matches []resolvedMatch
	var bridgeErrors []string
	for _, s := range a.allSurfaces() {
		sess, err := s.Resolve(ctx, target)
		if err == nil {
			matches = append(matches, resolvedMatch{session: sess, adapter: s})
			continue
		}
		if strings.Contains(err.Error(), "connect Codex Desktop") ||
			strings.Contains(err.Error(), "sidecar") ||
			strings.Contains(err.Error(), "no local transcript") {
			bridgeErrors = append(bridgeErrors, fmt.Sprintf("[%s] %s", s.Name(), err))
		}
	}
	if len(matches) > 1 {
		labels := make([]string, 0, len(matches))
		for _, candidate := range matches {
			labels = append(labels, fmt.Sprintf("%s:%s", candidate.adapter.Name(), candidate.session.ID))
		}
		return nil, nil, fmt.Errorf("ambiguous target %q matched %s; qualify it as surface:target", target, strings.Join(labels, ", "))
	}
	if len(matches) == 1 {
		candidate := matches[0]
		if a.registry() != nil {
			if err := a.registry().RegisterSession(*candidate.session); err != nil {
				return nil, nil, fmt.Errorf("register %s session: %w", candidate.adapter.Name(), err)
			}
		}
		return candidate.session, candidate.adapter, nil
	}
	if len(bridgeErrors) > 0 {
		return nil, nil, fmt.Errorf("%s\nno session matched '%s' (bridge may be down)", strings.Join(bridgeErrors, "; "), target)
	}
	return nil, nil, targetNotFoundError{target: target}
}

func (a *App) ensureWritableTarget(ctx context.Context, session *surface.Session, adapter surface.Surface) error {
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return err
	}
	isSyntheticNotion := session.Surface == surface.KindNotion && (session.ID == "new" || strings.HasPrefix(session.ID, "new:"))
	if a.registry() != nil && !isSyntheticNotion {
		if err := a.registry().RegisterSession(*session); err != nil {
			return fmt.Errorf("register %s session: %w", adapter.Name(), err)
		}
	}
	return nil
}

func (a *App) prepareDaemonSessionStream(ctx context.Context, adapter surface.Surface, session *surface.Session) (sessionstream.Subscription, error) {
	page, err := a.readSessionPage(ctx, adapter, session, surface.SessionReadRequest{Limit: 1})
	if err != nil {
		return sessionstream.Subscription{}, fmt.Errorf("capture active daemon stream cursor: %w", err)
	}
	if page.UnavailableReason != "" {
		return sessionstream.Subscription{}, fmt.Errorf("active daemon session source unavailable: %s", page.UnavailableReason)
	}
	var reader sessionStreamReader
	if a.daemonSessionStreamReader != nil {
		reader, err = a.daemonSessionStreamReader()
	} else {
		reader, err = newDaemonSessionStreamClient()
	}
	if err != nil {
		return sessionstream.Subscription{}, fmt.Errorf("open active daemon session stream: %w", err)
	}
	if reader == nil {
		return sessionstream.Subscription{}, errors.New("open active daemon session stream: reader is unavailable")
	}
	return reader.OpenSessionStream(ctx, session, page.JournalSeq)
}

func streamEventDelta(event sessionstream.Event, bodies map[string]string, versions map[string]uint64) string {
	if event.ItemID == "" {
		return event.Body
	}
	if prior, found := versions[event.ItemID]; found && ((event.Version > 0 && prior > 0 && event.Version <= prior) || (event.Version == 0 && bodies[event.ItemID] == event.Body)) {
		return ""
	}
	versions[event.ItemID] = event.Version
	previous := bodies[event.ItemID]
	bodies[event.ItemID] = event.Body
	if strings.HasPrefix(event.Body, previous) {
		return strings.TrimPrefix(event.Body, previous)
	}
	return event.Body
}

func directStreamEventDelta(event surface.StreamEvent, bodies map[string]string, versions map[string]uint64) string {
	key := event.ID
	if key == "" {
		key = event.ProviderKey
	}
	if key == "" {
		return event.Text
	}
	if prior, found := versions[key]; found && event.Version > 0 && prior > 0 && event.Version <= prior {
		return ""
	}
	versions[key] = event.Version
	previous := bodies[key]
	if event.Operation == "append" {
		bodies[key] = previous + event.Text
		return event.Text
	}
	bodies[key] = event.Text
	return surface.StreamEventDelta(event, previous)
}

func printSessionStreamEvent(event sessionstream.Event, bodies map[string]string, versions map[string]uint64) {
	if event.Kind == "tool_use" || event.Kind == "toolCall" || event.Kind == "tool_call" {
		if text := streamEventDelta(event, bodies, versions); text != "" {
			fmt.Printf("  -> %s\n", text)
		}
		return
	}
	if event.Kind == "tool_result" || event.Kind == "toolResult" {
		if text := streamEventDelta(event, bodies, versions); text != "" {
			fmt.Printf("  <- %s\n", text)
		}
		return
	}
	if !event.SpokenAssistantContent() {
		return
	}
	if text := streamEventDelta(event, bodies, versions); text != "" {
		fmt.Print(text)
	}
}

func directStreamTerminalError(event surface.StreamEvent, turnID string) error {
	if !event.Failed() {
		return nil
	}
	return fmt.Errorf("turn %s did not complete successfully: %s", turnID, event.FailureReason())
}

func streamDirectOutput(ctx context.Context, surf surface.Surface, sess *surface.Session, turnID string, timeout time.Duration) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	bodies := map[string]string{}
	versions := map[string]uint64{}
	var terminalErr error
	err := surf.Stream(streamCtx, sess, turnID, func(event surface.StreamEvent) {
		switch event.Class() {
		case surface.StreamEventMessage:
			if event.Role != "" && event.Role != "assistant" {
				return
			}
			if text := directStreamEventDelta(event, bodies, versions); text != "" {
				fmt.Print(text)
			}
		case surface.StreamEventToolCall:
			if text := event.Text; text != "" {
				fmt.Printf("  -> %s\n", text)
			}
		case surface.StreamEventToolResult:
			if text := event.Text; text != "" {
				fmt.Printf("  <- %s\n", text)
			}
		case surface.StreamEventTerminal:
			if err := directStreamTerminalError(event, turnID); err != nil {
				terminalErr = err
				cancel()
				return
			}
			fmt.Println()
		}
	}, timeout)
	if terminalErr != nil {
		return terminalErr
	}
	return err
}

func consumeSessionReply(ctx context.Context, subscription sessionstream.Subscription, turnID string) (*surface.ReplyResult, error) {
	bodies := map[string]string{}
	versions := map[string]uint64{}
	var latest string
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-subscription.Events:
			if !ok {
				return nil, fmt.Errorf("session stream ended before turn %s completed", turnID)
			}
			if event.Kind == "source-error" {
				return nil, fmt.Errorf("session source failed: %s", event.Reason)
			}
			if event.TurnID != turnID {
				continue
			}
			if event.SpokenAssistantContent() {
				if prior := bodies[event.ItemID]; event.Version == 0 || event.Version > versions[event.ItemID] {
					if event.ItemID != "" && strings.HasPrefix(event.Body, prior) {
						bodies[event.ItemID] = event.Body
					} else if event.ItemID != "" {
						bodies[event.ItemID] = event.Body
					}
					versions[event.ItemID] = event.Version
					latest = event.Body
				}
			}
			if event.Terminal() {
				if event.Failed() {
					return nil, fmt.Errorf("turn %s did not complete successfully: %s", turnID, event.FailureReason())
				}
				return &surface.ReplyResult{Text: latest, Done: true, Source: "daemon-journal"}, nil
			}
		}
	}
}

func consumeDaemonStreamOutput(ctx context.Context, subscription sessionstream.Subscription, turnID string, timeout time.Duration) error {
	bodies := map[string]string{}
	versions := map[string]uint64{}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for stream after %s: %w", timeout, ctx.Err())
		case event, ok := <-subscription.Events:
			if !ok {
				return fmt.Errorf("session stream ended before turn %s completed", turnID)
			}
			if event.Kind == "source-error" {
				return fmt.Errorf("session source failed: %s", event.Reason)
			}
			if event.TurnID != turnID {
				continue
			}
			if event.Kind == "done" {
				if event.Failed() {
					return fmt.Errorf("turn %s did not complete successfully: %s", turnID, event.FailureReason())
				}
				fmt.Println()
				return nil
			}
			printSessionStreamEvent(event, bodies, versions)
		}
	}
}

func (a *App) cmdSend(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf(`usage: agenthail send <target> "message" [--from <name>] [--stream] [--reply] [--json]`)
	}
	target := positional[0]
	message, err := messageText(strings.Join(positional[1:], " "))
	if err != nil {
		return err
	}
	if positional[1] != "-" && len(message) > 8000 {
		fmt.Fprintf(os.Stderr, "warning: message is %d chars; shell may have truncated it. Use '-' to read from stdin for long messages:\n  echo '...' | agenthail send %s -\n  agenthail send %s - < file.txt\n", len(message), target, target)
	}
	if message == "" {
		return fmt.Errorf("message is empty")
	}
	wantStream := hasFlag(args, "--stream")
	wantReply := hasFlag(args, "--reply")
	jsonOut := hasFlag(args, "--json")
	if wantStream && wantReply {
		return fmt.Errorf("--stream and --reply cannot be combined; choose live deltas or the completed reply")
	}
	if wantStream && jsonOut {
		return fmt.Errorf("--stream and --json cannot be combined; JSON event streaming is not implemented")
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fromLabel := flagVal(args, "--from")
	if fromLabel != "" {
		message = fmt.Sprintf("[from %s] %s", fromLabel, message)
	}

	_, peerLead, err := a.leadTarget(target)
	if err != nil {
		return err
	}
	if peerLead != nil && !hasFlag(args, "--stream") && !hasFlag(args, "--reply") {
		return a.sendToPeerCoordinator(ctx, *peerLead, message, args)
	}
	if target, err = a.expandLead(target); err != nil {
		return err
	}
	coord := a.coordinator()
	_, byName := coord.Named(target)
	if spec, named := coord.Named(target); named && !coord.Started(spec) {
		return a.startCoordinator(ctx, coord, spec, message, args)
	}
	sess, surf, remote, err := a.resolveDeliveryTarget(ctx, target)
	if err != nil {
		return err
	}
	if remote != nil {
		return a.sendRemote(ctx, remote, message, args, timeout)
	}
	spec, coordinated := coord.Of(sess.ID)
	if current, replaced := coord.ReplacedBy(sess.ID); replaced {
		if sess, surf, _, err = a.resolveAnyTarget(ctx, current); err != nil {
			return err
		}
	}
	if !surf.Capabilities().Send {
		return fmt.Errorf("%s does not support send", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	if surface.RouteFor(surf, sess, "") == surface.RoutePeer && (wantStream || wantReply) {
		return fmt.Errorf("Claude peer message IDs cannot be correlated with transcript turns; use 'last' or native SendMessage replies")
	}

	if wantStream && !surf.Capabilities().Stream {
		return fmt.Errorf("%s does not support stream", surf.Name())
	}
	baseline := ""
	var daemonStream sessionstream.Subscription
	useDaemonStream := a.daemonIsRunning() && (wantStream || wantReply)
	if useDaemonStream {
		daemonStream, err = a.prepareDaemonSessionStream(ctx, surf, sess)
		if err != nil {
			return err
		}
		if daemonStream.Cancel != nil {
			defer daemonStream.Cancel()
		}
	} else if wantReply {
		observation, observeErr := surf.Observe(ctx, sess)
		if observeErr != nil {
			return fmt.Errorf("establish reply cursor before send: %w", observeErr)
		}
		if observation != nil {
			baseline = observation.CompletedTurnID
		}
	}
	turnOptions, err := parseTurnOptions(args)
	if err != nil {
		return err
	}
	options := surface.SendOptions{Model: flagVal(args, "--model"), TurnOptions: turnOptions}
	options.SourceSessionID, err = a.sourceSessionID(ctx, fromLabel)
	if err != nil {
		return err
	}
	addressed := *sess
	dispatcher := delivery.Dispatcher{Registry: a.registry()}
	var receipt *delivery.Receipt
	syntheticNotion := surf.Name() == surface.KindNotion && (sess.ID == "new" || strings.HasPrefix(sess.ID, "new:"))
	syntheticNotionName := ""
	var notionName registry.AliasReservation
	if syntheticNotion {
		syntheticNotionName = sess.Name
		if a.registry() != nil {
			if notionName, err = a.registry().ReserveAlias(syntheticNotionName, time.Now().Add(timeout+threadNameMargin)); err != nil {
				return fmt.Errorf("reserve thread alias: %w", err)
			}
			defer func() { _ = a.registry().ReleaseAliasReservation(notionName) }()
		}
	}
	if coordinated {
		deliver := coord.Deliver
		if hasFlag(args, "--no-queue") {
			deliver = coord.DeliverWithoutQueue
		}
		var outcome *coordinator.Outcome
		if outcome, err = deliver(ctx, spec, message, options); err == nil {
			if outcome.Started {
				if !byName {
					return printCoordinatorStart(outcome, outcome.Session.ID, jsonOut)
				}
				return printCoordinatorStart(outcome, target, jsonOut)
			}
			receipt = outcome.Receipt
			if outcome.Session != nil && outcome.Session.ID != addressed.ID {
				if wantStream || wantReply {
					fmt.Fprintf(os.Stderr, "the coordinator moved to %s while this message was sent, so this send cannot follow its turns; read the reply with 'agenthail last %s'\n", outcome.Session.ID, outcome.Session.ID)
					wantStream, wantReply = false, false
				}
				sess = outcome.Session
			}
		}
	} else if hasFlag(args, "--no-queue") || syntheticNotion {
		receipt, err = dispatcher.DeliverWithoutQueue(ctx, surf, sess, message, "", options)
	} else {
		receipt, err = dispatcher.DeliverWithOptions(ctx, surf, sess, message, "", options)
	}
	if err != nil {
		if syntheticNotion && errors.Is(err, delivery.ErrTargetBusy) {
			return fmt.Errorf("a new Notion thread must be created immediately and cannot be queued; retry 'agenthail send %s ...'", target)
		}
		return err
	}

	if receipt.Evidence == surface.EvidenceQueued {
		if _, ok := daemon.IsRunning(); !ok {
			fmt.Fprintf(os.Stderr, "warning: daemon is not running; queued message will not be delivered until you start it (agenthail daemon start)\n")
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(receipt)
		} else {
			if receipt.WaitingOn != nil {
				fmt.Printf("queued #%d for %s, which is waiting on you; it delivers after you answer\n", receipt.QueueID, a.resolveDisplay(sess.ID))
			} else {
				steerTarget := target
				if coordinated && !byName {
					steerTarget = sess.ID
				}
				fmt.Printf("target is active; queued #%d for %s and will deliver when the current turn finishes (use 'agenthail steer %s \"message\"' to affect the current turn)\n", receipt.QueueID, a.resolveDisplay(sess.ID), steerTarget)
			}
		}
		return nil
	}
	if receipt.Status == string(registry.DeliveryIntentSubmitted) {
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(receipt)
		}
		if receipt.DeliveryID != 0 {
			fmt.Printf("delivery submitted (id %d); %s\n", receipt.DeliveryID, receipt.Detail)
		} else {
			fmt.Printf("delivery submitted; %s\n", receipt.Detail)
		}
		return nil
	}
	if syntheticNotion && receipt.TurnID != "" {
		sess.ID = receipt.TurnID
		sess.Name = syntheticNotionName
		receipt.SessionID = sess.ID
		if a.registry() != nil {
			if err := a.registry().RegisterSession(*sess); err != nil {
				return fmt.Errorf("Notion thread %s was created but could not be registered: %w", sess.ID, err)
			}
			if err := a.registry().BindReservedAlias(notionName, sess.ID); err != nil {
				return fmt.Errorf("Notion thread %s was created but alias %q could not be registered: %w", sess.ID, syntheticNotionName, err)
			}
		}
	}

	if (wantStream || wantReply) && sess.Surface == surface.KindClaude && (sess.ID != addressed.ID || surface.RouteFor(surf, sess, options.Author).TransportOnly()) {
		fmt.Fprintf(os.Stderr, "the closed session was resumed as %s, whose turns this send cannot follow; read the reply with 'agenthail last %s'\n", sess.ID, target)
		wantStream, wantReply = false, false
	}
	if wantStream {
		if useDaemonStream {
			return consumeDaemonStreamOutput(ctx, daemonStream, receipt.TurnID, timeout)
		}
		return streamDirectOutput(ctx, surf, sess, receipt.TurnID, timeout)
	}

	if wantReply {
		var reply *surface.ReplyResult
		if useDaemonStream {
			reply, err = consumeSessionReply(ctx, daemonStream, receipt.TurnID)
		} else {
			reply, err = waitForReply(ctx, surf, sess, baseline, receipt.TurnID, timeout)
		}
		if err != nil {
			return err
		}
		receipt.Evidence = surface.EvidenceReplyObserved
		if a.registry() != nil {
			text := ""
			if reply != nil {
				text = reply.Text
			}
			_ = a.registry().RecordHistory(registry.HistoryEntry{Kind: "reply", SessionID: sess.ID, CompletionID: receipt.TurnID, Result: text})
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"delivery": receipt, "reply": reply})
		}
		if reply != nil && reply.Text != "" {
			fmt.Println(reply.Text)
		}
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(receipt)
	} else if receipt.Evidence == surface.EvidenceTransportAccepted {
		fmt.Printf("accepted by Claude socket (message %s); receiver policy and model completion are pending\n", receipt.TurnID)
	} else {
		fmt.Printf("sent (turn %s)\n", receipt.TurnID)
	}
	if receipt.Notice != "" && !jsonOut {
		fmt.Println(receipt.Notice)
	}
	return nil
}

func waitForReply(ctx context.Context, adapter surface.Surface, session *surface.Session, baseline, turnID string, timeout time.Duration) (*surface.ReplyResult, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	seenActiveTurn := false
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for reply after %s: %w", timeout, ctx.Err())
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for reply after %s", timeout)
		case <-ticker.C:
			observation, err := adapter.Observe(ctx, session)
			if err != nil {
				return nil, err
			}
			if observation != nil && observation.ActiveTurnID != "" && (turnID == "" || observation.ActiveTurnID == turnID) {
				seenActiveTurn = true
			}
			if observation != nil && adapter.Name() == surface.KindCodex && turnID != "" && observation.TerminalTurnID == turnID && observation.CompletedTurnID != turnID {
				return nil, fmt.Errorf("turn %s ended without an assistant reply", turnID)
			}
			if seenActiveTurn && observation != nil && observation.Status == surface.StatusIdle && observation.ActiveTurnID == "" && observation.CompletedTurnID == baseline {
				return nil, fmt.Errorf("turn %s ended without a completed assistant reply", turnID)
			}
			if observation == nil || observation.Reply == nil || !observation.Reply.Done {
				continue
			}
			if observation.CompletedTurnID == baseline {
				continue
			}
			if observation.Reply.Error != "" {
				return nil, fmt.Errorf("turn %s did not complete successfully: %s", observation.CompletedTurnID, observation.Reply.Error)
			}
			if turnID != "" && observation.CompletedTurnID != turnID && adapter.Name() == surface.KindCodex {
				continue
			}
			return observation.Reply, nil
		}
	}
}

func commandTimeout(args []string, fallback time.Duration) (time.Duration, error) {
	value := flagVal(args, "--timeout")
	if value == "" {
		return fallback, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("--timeout must be a positive duration such as 30s or 2m")
	}
	return timeout, nil
}

func sessionReadBefore(args []string) (int64, error) {
	value := flagVal(args, "--before")
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor <= 0 {
		return 0, fmt.Errorf("--before must be a positive activity cursor")
	}
	return cursor, nil
}

func (a *App) cmdReply(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail reply <target> [--before cursor] [--json] [--max-bytes N | --report] [--timeout 30s]")
	}
	mode, err := readModeFlags(args)
	if err != nil {
		return err
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, surf, remote, err := a.resolveReadTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		sess = remote.session()
	} else if !surface.CapabilitiesFor(surf, sess).Reply {
		return fmt.Errorf("%s does not support reply", surf.Name())
	}
	before, err := sessionReadBefore(args)
	if err != nil {
		return err
	}
	read, err := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: 2, Before: before})
	if err != nil {
		return err
	}
	if read.UnavailableReason != "" && len(read.Exchanges) == 0 {
		return fmt.Errorf("session read unavailable from %s: %s", read.Source, read.UnavailableReason)
	}
	reply := read.Reply
	if reply == nil {
		return fmt.Errorf("%s returned an empty reply result", sess.Surface)
	}
	if reply.Error != "" {
		return fmt.Errorf("latest %s turn did not complete successfully: %s", sess.Surface, reply.Error)
	}
	if mode.report {
		parsed, err := report.Parse(reply.Text)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "source": read.Source, "done": reply.Done, "report": parsed, "turn": replyTurn(read.Exchanges, reply)})
	}
	text, cut := capBytes(reply.Text, mode.maxBytes)
	if hasFlag(args, "--json") {
		document := map[string]any{"surface": sess.Surface, "session": sess.ID, "text": text, "done": reply.Done, "source": read.Source, "journalSeq": read.JournalSeq, "nextBefore": read.NextBefore, "readError": read.UnavailableReason, "warning": read.Warning, "turn": replyTurn(read.Exchanges, reply)}
		if cut {
			document["truncated"], document["textBytes"] = true, len(reply.Text)
		}
		return json.NewEncoder(os.Stdout).Encode(document)
	} else {
		printSessionReadWarning(read)
		if read.Source != "" {
			fmt.Printf("[%s]\n", read.Source)
		}
		fmt.Println(text)
		if cut {
			fmt.Println(truncationNote(len(text), len(reply.Text)))
		}
		if read.NextBefore > 0 {
			fmt.Printf("older cursor: %d\n", read.NextBefore)
		}
	}
	return nil
}

func (a *App) cmdLast(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: agenthail last <target> [count] [--before cursor] [--full] [--json] [--max-bytes N | --report] [--timeout 30s]")
	}
	mode, err := readModeFlags(args)
	if err != nil {
		return err
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, surf, remote, err := a.resolveReadTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		sess = remote.session()
	}
	n := 1
	if len(positional) > 1 {
		v, parseErr := strconv.Atoi(positional[1])
		if parseErr != nil || v < 1 || v > surface.MaxExchangePage {
			return fmt.Errorf("count must be an integer from 1 to %d", surface.MaxExchangePage)
		}
		n = v
	}
	before, err := sessionReadBefore(args)
	if err != nil {
		return err
	}
	read, err := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: n, Before: before})
	if err != nil {
		return err
	}
	exchanges := append([]surface.Exchange{}, read.Exchanges...)
	for pages := 1; len(exchanges) < n && read.NextBefore > 0 && pages < 10; pages++ {
		older, olderErr := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: n - len(exchanges), Before: read.NextBefore})
		if olderErr != nil {
			break
		}
		exchanges = append(append([]surface.Exchange{}, older.Exchanges...), exchanges...)
		read.NextBefore = older.NextBefore
	}
	if len(exchanges) > n {
		exchanges = exchanges[len(exchanges)-n:]
	}
	if read.UnavailableReason != "" && len(exchanges) == 0 {
		return fmt.Errorf("session read unavailable from %s: %s", read.Source, read.UnavailableReason)
	}
	if mode.report {
		return printExchangeReports(sess, read, exchanges)
	}
	capped := make([]cappedExchange, len(exchanges))
	for i, exchange := range exchanges {
		capped[i] = capExchange(exchange, mode.maxBytes)
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "source": read.Source, "journalSeq": read.JournalSeq, "nextBefore": read.NextBefore, "readError": read.UnavailableReason, "warning": read.Warning, "exchanges": capped})
	}
	printSessionReadWarning(read)
	if len(exchanges) == 0 {
		fmt.Println("(no conversation history)")
		return nil
	}
	label := a.resolveDisplay(sess.ID)
	if remote != nil {
		label = remote.label()
	}
	source := read.Source
	if source != "" {
		fmt.Printf("── %s [%s] ──\n", label, source)
	} else {
		fmt.Printf("── %s ──\n", label)
	}
	full := hasFlag(args, "--full") || mode.maxBytes > 0
	for _, ex := range capped {
		if !ex.Timestamp.IsZero() {
			fmt.Printf("%s\n", ex.Timestamp.Local().Format("2006-01-02 15:04:05"))
		}
		if ex.User != "" || ex.UserTruncated {
			if full {
				fmt.Printf("%s ▸ %s\n", promptLabel(ex.Exchange), ex.User)
				if ex.UserTruncated {
					fmt.Println(truncationNote(len(ex.User), ex.UserBytes))
				}
			} else {
				fmt.Printf("%s ▸ %s\n", promptLabel(ex.Exchange), truncate(strings.ReplaceAll(ex.User, "\n", " "), 200))
			}
		}
		if ex.Assistant != "" || ex.AssistantTruncated {
			if full {
				fmt.Printf(" ai  ▸ %s\n", ex.Assistant)
				if ex.AssistantTruncated {
					fmt.Println(truncationNote(len(ex.Assistant), ex.AssistantBytes))
				}
			} else {
				fmt.Printf(" ai  ▸ %s\n", truncate(strings.ReplaceAll(ex.Assistant, "\n", " "), 200))
			}
		}
		fmt.Println()
	}
	if read.NextBefore > 0 {
		fmt.Printf("older cursor: %d\n", read.NextBefore)
	}
	return nil
}

// promptLabel names who sent an exchange's prompt, so an injected prompt is
// never shown as the person's own words.
func promptLabel(exchange surface.Exchange) string {
	switch exchange.Origin {
	case "":
		return "you "
	case "peer":
		if exchange.Sender != "" {
			return "from " + exchange.Sender
		}
		return "agent"
	case "task-notification":
		return "task notification"
	case "auto-continuation":
		return "continued"
	}
	return exchange.Origin
}

func printSessionReadWarning(read *surface.SessionReadResult) {
	if read.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", read.Warning)
	}
	if read.UnavailableReason != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", read.UnavailableReason)
	}
}

func readSessionWithContext(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	type result struct {
		read *surface.SessionReadResult
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		read, err := surface.ReadSession(ctx, adapter, session, request)
		completed <- result{read: read, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case outcome := <-completed:
		return outcome.read, outcome.err
	}
}

// offlineSessionPageItems is the provider page read without a daemon; provider pages count items.
const offlineSessionPageItems = 40

func (a *App) readSessionPage(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	if a.daemonIsRunning() {
		var reader sessionPageReader
		var err error
		if a.daemonSessionPageReader != nil {
			reader, err = a.daemonSessionPageReader()
		} else {
			reader, err = newDaemonSessionPageClient()
		}
		if err != nil {
			return nil, fmt.Errorf("read active daemon session page: %w", err)
		}
		if reader == nil {
			return nil, errors.New("read active daemon session page: reader is unavailable")
		}
		read, err := reader.ReadSession(ctx, session, request)
		if err != nil {
			return nil, err
		}
		if read == nil {
			return nil, errors.New("read active daemon session page: empty response")
		}
		return surface.BoundSessionRead(session, read), nil
	}
	request.Limit = offlineSessionPageItems
	read, err := readSessionWithContext(ctx, adapter, session, request)
	if err != nil {
		return nil, err
	}
	if read != nil && read.Source == "" {
		read.Source = string(adapter.Name()) + "-offline-provider"
	}
	return read, nil
}

func (a *App) cmdStream(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail stream <target>")
	}
	timeout, err := commandTimeout(args, 10*time.Minute)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		sess = remote.session()
	} else if !surface.CapabilitiesFor(surf, sess).Stream {
		return fmt.Errorf("%s does not support stream", surf.Name())
	}
	if a.daemonIsRunning() {
		page, err := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: 1})
		if err != nil {
			return fmt.Errorf("read active daemon stream cursor: %w", err)
		}
		if page.UnavailableReason != "" {
			return fmt.Errorf("active daemon session source unavailable: %s", page.UnavailableReason)
		}
		var reader sessionStreamReader
		if a.daemonSessionStreamReader != nil {
			reader, err = a.daemonSessionStreamReader()
		} else {
			reader, err = newDaemonSessionStreamClient()
		}
		if err != nil {
			return fmt.Errorf("open active daemon session stream: %w", err)
		}
		if reader == nil {
			return errors.New("open active daemon session stream: reader is unavailable")
		}
		subscription, err := reader.OpenSessionStream(ctx, sess, 0)
		if err != nil {
			return err
		}
		defer subscription.Cancel()
		bodies := map[string]string{}
		versions := map[string]uint64{}
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case event, ok := <-subscription.Events:
				if !ok {
					return errors.New("daemon session stream ended")
				}
				if event.Kind == "source-error" {
					return fmt.Errorf("session source failed: %s", event.Reason)
				}
				if event.Seq <= page.JournalSeq {
					continue
				}
				printSessionStreamEvent(event, bodies, versions)
			}
		}
	}
	return streamDirectOutput(ctx, surf, sess, "", timeout)
}

func (a *App) cmdCompact(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail compact <target>")
	}
	ctx := context.Background()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.compactRemote(ctx, remote)
	}
	if !surface.EffectiveCapabilities(surf, sess).Compact {
		return fmt.Errorf("%s does not support compact", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	receipt, err := (delivery.Dispatcher{Registry: a.registry()}).Compact(ctx, surf, sess)
	if err != nil {
		return err
	}
	if receipt.Evidence == surface.EvidenceQueued {
		fmt.Printf("compact queued for %s as #%d; it will run when the target is idle\n", a.resolveDisplay(sess.ID), receipt.QueueID)
		return nil
	}
	fmt.Printf("compacted %s\n", a.resolveDisplay(sess.ID))
	return nil
}

func (a *App) cmdModel(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: agenthail model <target> [name] [--json] [--timeout 30s]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		name := ""
		if len(positional) > 1 {
			name = positional[1]
		}
		return a.modelRemote(ctx, remote, name, args)
	}
	if !surface.EffectiveCapabilities(surf, sess).Model {
		return fmt.Errorf("%s does not support model switching", surf.Name())
	}
	name := ""
	if len(positional) > 1 {
		name = positional[1]
		if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
			return err
		}
	}
	if controller, ok := surf.(surface.Controller); ok && name != "" {
		result, controlErr := controller.Control(ctx, sess, surface.ControlRequest{Action: "model", Value: name})
		if controlErr == nil {
			a.recordHistory(registry.HistoryEntry{Kind: "model", SessionID: sess.ID, Message: name, Result: "changed by " + daemon.CLIActor})
		}
		if controlErr == nil && a.registry() != nil {
			if err := a.registry().RegisterSession(*sess); err != nil {
				return fmt.Errorf("persist Claude model state: %w", err)
			}
		}
		return a.printControlResult(sess, result, controlErr, args)
	}
	if setter, ok := surf.(surface.ModelSetter); ok && name != "" {
		change, err := setter.SetModel(ctx, sess, name)
		if err != nil {
			return err
		}
		a.recordHistory(registry.HistoryEntry{Kind: "model", SessionID: sess.ID, Message: name, Result: change.Text()})
		return printModelChange(sess.ID, change, args)
	}
	current, err := surf.Model(ctx, sess, name)
	if err != nil {
		return err
	}
	change := surface.ModelChange{Model: current}
	if reader, ok := surf.(surface.PendingModelReader); ok && name == "" {
		change.PendingModel = reader.PendingModel(sess.ID)
	}
	return printModelChange(sess.ID, change, args)
}

func printModelChange(sessionID string, change surface.ModelChange, args []string) error {
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(struct {
			SessionID string `json:"sessionId"`
			surface.ModelChange
		}{sessionID, change})
	}
	if change.Model != "" {
		fmt.Println(change.Text())
	}
	return nil
}

func (a *App) cmdControl(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail control <target> <context|usage|status|effort|rename> [value] [--json] [--timeout 30s]")
	}
	action, value := positional[1], strings.Join(positional[2:], " ")
	switch action {
	case "context", "usage", "status", "effort", "rename":
	default:
		return fmt.Errorf("unknown control %q; use model, interrupt, steer or compact for those", action)
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.controlRemote(ctx, remote, action, value, args)
	}
	controller, ok := surf.(surface.Controller)
	if !ok || !surface.EffectiveCapabilities(surf, sess).Commands {
		return fmt.Errorf("%s does not support Claude Remote Control commands", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	result, controlErr := controller.Control(ctx, sess, surface.ControlRequest{Action: action, Value: value})
	return a.printControlResult(sess, result, controlErr, args)
}

// printControlResult shows a control's evidence and request id, also when the
// control failed or its outcome is unknown, and records it in history.
func (a *App) printControlResult(sess *surface.Session, result *surface.ControlResult, controlErr error, args []string) error {
	if result == nil {
		return controlErr
	}
	(delivery.Dispatcher{Registry: a.registry()}).RecordControl(result, controlErr)
	if hasFlag(args, "--json") {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			return err
		}
		return controlErr
	}
	printControl(a.resolveDisplay(sess.ID), result)
	return controlErr
}

func printControl(target string, result *surface.ControlResult) {
	request := ""
	if result.RequestID != "" {
		request = " (request " + result.RequestID + ")"
	}
	fmt.Printf("%s %s for %s%s\n", result.Action, result.Evidence, target, request)
	if result.Detail != "" {
		fmt.Println(result.Detail)
	}
	if result.Output != "" {
		fmt.Println(result.Output)
	}
}

func commandContext(args []string) (context.Context, context.CancelFunc, error) {
	timeout, err := commandTimeout(args, 30*time.Second)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return ctx, cancel, nil
}

func (a *App) cmdInterrupt(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail interrupt <target> [--json] [--timeout 30s]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.interruptRemote(ctx, remote, args)
	}
	if !surface.EffectiveCapabilities(surf, sess).Interrupt {
		return fmt.Errorf("%s does not support interrupt", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	if controller, ok := surf.(surface.Controller); ok {
		result, err := controller.Control(ctx, sess, surface.ControlRequest{Action: "interrupt"})
		return a.printControlResult(sess, result, err, args)
	}
	return surf.Interrupt(ctx, sess)
}

func (a *App) cmdSteer(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail steer <target> \"message\" ('-' reads stdin) [--json] [--timeout 30s]")
	}
	ctx, cancel, err := commandContext(args)
	if err != nil {
		return err
	}
	defer cancel()
	sess, surf, remote, err := a.resolveDeliveryTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return a.steerRemote(ctx, remote, strings.Join(positional[1:], " "), hasFlag(args, "--json"))
	}
	if !surface.EffectiveCapabilities(surf, sess).Steer {
		return fmt.Errorf("%s does not support steer", surf.Name())
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	sourceID, err := a.sourceSessionID(ctx, "")
	if err != nil {
		return err
	}
	if sourceID != "" {
		ctx = surface.WithSourceSessionID(ctx, sourceID)
	}
	message, err := messageText(strings.Join(positional[1:], " "))
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message is empty")
	}
	receipt, err := (delivery.Dispatcher{Registry: a.registry()}).Steer(ctx, surf, sess, message)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(receipt)
	}
	printSteerReceipt(*receipt, a.resolveDisplay(sess.ID))
	return nil
}

func printSteerReceipt(receipt delivery.Receipt, target string) {
	switch {
	case receipt.Evidence == surface.EvidenceQueued:
		fmt.Printf("Queued #%d for %s, which is waiting on you; it delivers after you answer.\n", receipt.QueueID, target)
	case receipt.Status == string(registry.DeliveryIntentSubmitted):
		fmt.Printf("Submitted to %s.\n", target)
	case receipt.Evidence == surface.EvidenceTransportAccepted:
		fmt.Printf("Steer accepted for %s (request %s); not yet confirmed in the current turn.\n", target, receipt.RequestID)
	default:
		fmt.Printf("Sent to %s.\n", target)
	}
}

const queueUsage = `usage: agenthail queue <target> "message"
       agenthail queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>] [--cwd <path>]
       agenthail queue retry <id>
       agenthail queue rm <id>
       agenthail queue clear <target>`

func (a *App) cmdQueue(args []string) error {
	if a.registry() == nil {
		return fmt.Errorf("queue requires the registry")
	}
	if len(args) > 0 && args[0] == "list" {
		if hasFlag(args, "--help") {
			fmt.Println("usage: agenthail queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>] [--cwd <path>]")
			fmt.Println("  --target   show messages for one destination")
			fmt.Println("  --mine     show messages sent by or addressed to the caller session")
			fmt.Println("  --project  show messages for targets in one project")
			fmt.Println("  --cwd      show messages for targets in the project that holds this folder")
			fmt.Println("  --all     include terminal queue history")
			return nil
		}
		if len(stripFlags(args)) != 1 {
			return fmt.Errorf("usage: agenthail queue list [--json] [--all] [--target <target>] [--mine] [--project <name|id>] [--cwd <path>]")
		}
		rows, err := a.registry().ListQueue(hasFlag(args, "--all"), a.adapters())
		if err != nil {
			return err
		}
		rows, err = a.filterQueueRows(context.Background(), rows, flagVal(args, "--target"), hasFlag(args, "--mine"), strings.TrimSpace(flagVal(args, "--project")), flagVal(args, "--cwd"))
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"messages": rows})
		}
		if len(rows) == 0 {
			fmt.Println("(queue empty)")
			return nil
		}
		for _, row := range rows {
			hostID, _ := a.Registry.SessionHost(row.SessionID)
			fmt.Printf("#%-4d %-26s attempts=%d target=%s %s\n", row.ID, queueStatusLabel(row, hostID != ""), row.Attempts, a.resolveDisplay(row.SessionID), truncate(strings.ReplaceAll(row.Message, "\n", " "), 100))
			if row.Status == "pending" {
				fmt.Printf("      queued %s; %s\n", relTime(queuedTime(row.QueuedAt)), queueHoldLabel(row.HeldBy))
			}
			if row.LastError != "" {
				fmt.Printf("      last error: %s\n", row.LastError)
			}
		}
		return nil
	}
	if len(args) > 0 && args[0] == "retry" {
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail queue retry <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid queue id %q", args[1])
		}
		item, err := a.registry().QueueItem(id, a.adapters())
		if err != nil {
			return err
		}
		session, err := a.registry().Session(item.SessionID)
		if err != nil {
			return err
		}
		// A paired host's session is retried by the daemon replaying the
		// held request with its original key.
		if owner, ownerErr := a.registry().SessionHost(session.ID); ownerErr != nil || owner == "" {
			adapter, adapterErr := a.adapters().For(session)
			if adapterErr != nil {
				return adapterErr
			}
			if err := a.ensureWritableTarget(context.Background(), session, adapter); err != nil {
				return err
			}
		}
		if err := a.registry().RetryMessage(id); err != nil {
			return err
		}
		fmt.Printf("queue item #%d scheduled for retry\n", id)
		return nil
	}
	if len(args) > 0 && (args[0] == "rm" || args[0] == "cancel") {
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail queue rm <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid queue id %q", args[1])
		}
		if err := a.registry().CancelMessage(id); err != nil {
			return err
		}
		fmt.Printf("queue item #%d canceled\n", id)
		return nil
	}
	if len(args) > 0 && args[0] == "clear" {
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail queue clear <target>")
		}
		sessionID, err := a.queueSessionID(context.Background(), args[1])
		if err != nil {
			return err
		}
		count, err := a.registry().CancelMessagesForSession(sessionID)
		if err != nil {
			return err
		}
		fmt.Printf("canceled %d queued message(s) for %s\n", count, a.resolveDisplay(sessionID))
		return nil
	}
	positional := stripFlags(args)
	if len(positional) < 2 {
		return errors.New(queueUsage)
	}
	ctx := context.Background()
	sess, surf, remote, err := a.resolveDeliveryTarget(ctx, positional[0])
	if err != nil {
		return fmt.Errorf("%w\n%s", err, queueUsage)
	}
	if remote != nil {
		message, err := messageText(strings.Join(positional[1:], " "))
		if err != nil {
			return err
		}
		if message == "" {
			return fmt.Errorf("message is empty")
		}
		return a.queueRemote(ctx, remote, message)
	}
	if a.registry() == nil {
		return fmt.Errorf("queue requires the registry")
	}
	if surf.Name() == surface.KindNotion && (sess.ID == "new" || strings.HasPrefix(sess.ID, "new:")) {
		return fmt.Errorf("a new Notion thread cannot be queued before it has a persisted UUID; use 'agenthail send %s ...'", positional[0])
	}
	if err := a.ensureWritableTarget(ctx, sess, surf); err != nil {
		return err
	}
	message, err := messageText(strings.Join(positional[1:], " "))
	if err != nil {
		return err
	}
	if positional[1] != "-" && len(message) > 8000 {
		fmt.Fprintf(os.Stderr, "warning: message is %d chars; shell may have truncated it. Use '-' to read from stdin for long messages.\n", len(message))
	}
	if message == "" {
		return fmt.Errorf("message is empty")
	}
	if _, ok := daemon.IsRunning(); !ok {
		fmt.Fprintf(os.Stderr, "warning: daemon is not running; queued message will not be delivered until you start it (agenthail daemon start)\n")
	}
	if _, err := a.registry().QueueMessageWithOptions(sess.ID, message, "", surface.SendOptions{BusyDelivery: string(deliverypolicy.Queue)}); err != nil {
		return err
	}
	if waiting, err := a.registry().SessionWaiting(sess.ID); err == nil && waiting != nil {
		fmt.Printf("queued for %s, which is waiting on you; it delivers after you answer\n", a.resolveDisplay(sess.ID))
	} else {
		fmt.Printf("queued for %s (delivered when the target is idle on a daemon scan)\n", a.resolveDisplay(sess.ID))
	}
	return nil
}

func (a *App) filterQueueRows(ctx context.Context, rows []registry.QueueRow, targetSelector string, mine bool, projectSelector, cwd string) ([]registry.QueueRow, error) {
	targetID := ""
	if targetSelector != "" {
		sessionID, err := a.queueSessionID(ctx, targetSelector)
		if err != nil {
			return nil, fmt.Errorf("resolve --target: %w", err)
		}
		targetID = sessionID
	}
	mineID := ""
	if mine {
		var err error
		mineID, err = a.sourceSessionID(ctx, "")
		if err != nil {
			return nil, err
		}
		if mineID == "" {
			return nil, fmt.Errorf("--mine requires a caller session binding; set AGENTHAIL_SESSION_ID, CODEX_THREAD_ID, or CLAUDE_CODE_SESSION_ID")
		}
	}
	if projectSelector != "" && cwd != "" {
		return nil, fmt.Errorf("choose either --cwd or --project")
	}
	var views map[string]listCatalogView
	projectID := ""
	if projectSelector != "" || cwd != "" {
		snapshot, err := a.registry().CatalogSnapshot()
		if err != nil {
			return nil, fmt.Errorf("read daemon catalog: %w", err)
		}
		views = make(map[string]listCatalogView, len(snapshot.Sessions))
		for _, record := range snapshot.Sessions {
			views[record.Session.ID] = catalogListView(record)
		}
		if projectID, err = a.selectedProject(ctx, views, projectSelector, cwd); err != nil {
			return nil, err
		}
	}
	filtered := make([]registry.QueueRow, 0, len(rows))
	for _, row := range rows {
		if targetID != "" && row.SessionID != targetID {
			continue
		}
		if mineID != "" && row.SessionID != mineID && row.SourceSessionID != mineID {
			continue
		}
		if projectID != "" {
			if project := views[row.SessionID].Project; project == nil || project.ID != projectID {
				continue
			}
		}
		filtered = append(filtered, row)
	}
	return filtered, nil
}

func (a *App) cmdHistory(args []string) error {
	if a.registry() == nil {
		return fmt.Errorf("history requires the registry")
	}
	positional := stripFlags(args)
	if len(positional) > 2 {
		return fmt.Errorf("usage: agenthail history [target] [count] [--json]")
	}
	sessionID := ""
	limit := 50
	if len(positional) > 0 {
		if len(positional) == 2 {
			parsed, err := strconv.Atoi(positional[1])
			if err != nil || parsed < 1 {
				return fmt.Errorf("count must be a positive integer")
			}
			limit = parsed
		}
		sessionID = positional[0]
		if len(positional) == 1 {
			if parsed, err := strconv.Atoi(sessionID); err == nil {
				limit = parsed
				sessionID = ""
			}
		}
	}
	if limit > 200 {
		return fmt.Errorf("count must be 200 or less")
	}
	if sessionID != "" {
		ctx := context.Background()
		session, _, remote, err := a.resolveAnyTarget(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("resolve history target %q: %w", sessionID, err)
		}
		if remote != nil {
			return a.historyRemote(ctx, remote, limit, hasFlag(args, "--json"))
		}
		sessionID = session.ID
	}
	entries, err := a.registry().ListHistory(limit, sessionID)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"history": entries})
	}
	if len(entries) == 0 {
		fmt.Println("(no delivery history)")
		return nil
	}
	for _, entry := range entries {
		target := entry.SessionID
		if target != "" {
			target = a.resolveDisplay(target)
		}
		if entry.SourceSessionID != "" {
			fmt.Printf("%s %-18s %s -> %s", entry.CreatedAt, historyEvidenceLabel(entry), a.resolveDisplay(entry.SourceSessionID), target)
		} else {
			fmt.Printf("%s %-18s %s", entry.CreatedAt, historyEvidenceLabel(entry), target)
		}
		if entry.Message != "" {
			fmt.Printf(" %s", truncate(strings.ReplaceAll(entry.Message, "\n", " "), 140))
		}
		if entry.Error != "" {
			fmt.Printf(" error=%s", truncate(strings.ReplaceAll(entry.Error, "\n", " "), 140))
		}
		fmt.Println()
	}
	return nil
}

func historyEvidenceLabel(entry registry.HistoryEntry) string {
	if entry.Evidence != "" {
		return string(entry.Evidence)
	}
	return entry.Kind
}

func (a *App) cmdDoctor(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail doctor [--json]")
	}
	ctx := context.Background()
	type doctorResult struct {
		Surface      string                 `json:"surface"`
		Capabilities []string               `json:"capabilities"`
		Sessions     int                    `json:"sessions"`
		WritePath    string                 `json:"writePath"`
		OK           bool                   `json:"ok"`
		Error        string                 `json:"error,omitempty"`
		SigninOff    string                 `json:"signinOff,omitempty"`
		Runtime      *surface.RuntimeStatus `json:"runtime,omitempty"`
	}
	var results []doctorResult
	failures := 0
	for _, e := range a.Surfaces {
		caps := e.Surface.Capabilities()
		var enabled []string
		if caps.Send {
			enabled = append(enabled, "send")
		}
		if caps.Stream {
			enabled = append(enabled, "stream")
		}
		if caps.Reply {
			enabled = append(enabled, "reply")
		}
		if caps.Goal {
			enabled = append(enabled, "goal")
		}
		if caps.GoalSet {
			enabled = append(enabled, "goalSet")
		}
		if caps.GoalStatus {
			enabled = append(enabled, "goalStatus")
		}
		if caps.Compact {
			enabled = append(enabled, "compact")
		}
		if caps.Model {
			enabled = append(enabled, "model")
		}
		if caps.Interrupt {
			enabled = append(enabled, "interrupt")
		}
		if caps.Steer {
			enabled = append(enabled, "steer")
		}
		var healthErr error
		if checker, ok := e.Surface.(surface.HealthChecker); ok {
			healthErr = checker.Health(ctx)
		}
		var sessions []surface.Session
		var err error
		signinOff := ""
		if signin.IsOff(healthErr) {
			signinOff = healthErr.Error()
			healthErr = nil
		}
		if healthErr == nil {
			sessions, err = e.Surface.List(ctx)
		} else {
			err = healthErr
		}
		if signin.IsOff(err) {
			signinOff = err.Error()
			err = nil
		}
		result := doctorResult{Surface: e.Name, Capabilities: enabled, Sessions: len(sessions), WritePath: "checked immediately before delivery", OK: err == nil, SigninOff: signinOff}
		if provider, ok := e.Surface.(surface.RuntimeStatusProvider); ok {
			runtimeStatus := provider.RuntimeStatus(ctx)
			if runtimeStatus.Name != "" {
				if runtimeStatus.Reachable && !runtimeStatus.Durable && runtimeStatus.Backend == "pid" && a.isDaemonServiceLoaded() {
					runtimeStatus.Durable = true
					runtimeStatus.Problem = ""
					runtimeStatus.Detail = "supervised by Agenthail across reboot"
					runtimeStatus.Remediation = ""
				}
				result.Runtime = &runtimeStatus
				if runtimeStatus.Backend == "desktop" {
					result.WritePath = "direct input verified per target before delivery"
				}
				if !runtimeStatus.Reachable || !runtimeStatus.Durable {
					result.OK = false
					if err == nil {
						if runtimeStatus.Reachable {
							err = fmt.Errorf("%s is reachable but unsupervised", runtimeStatus.Name)
						} else if runtimeStatus.Detail != "" {
							err = fmt.Errorf("%s is unavailable: %s", runtimeStatus.Name, runtimeStatus.Detail)
						} else {
							err = fmt.Errorf("%s is unavailable", runtimeStatus.Name)
						}
					}
				}
			}
		}
		if err != nil {
			result.Error = err.Error()
			failures++
		}
		results = append(results, result)
	}
	signins := a.signinStatus(ctx)
	home, _ := os.UserHomeDir()
	claudeRoots, claudeRootsErr := surface.ClaudeConfigRoots(home)
	if claudeRootsErr != nil {
		failures++
	}
	promptHook := map[string]string{}
	for _, root := range claudeRoots {
		state, err := claudePromptHookState(filepath.Join(root, "settings.json"))
		if err != nil {
			state = claudePromptHookMissing
		}
		promptHook[root] = state
	}
	claudeConfigRoots, _ := surface.DescribeClaudeConfigRoots(home)
	service := a.service()
	serviceProblems := service.Problems()
	if serviceProblems == nil {
		serviceProblems = []serviceProblem{}
	}
	var grants *permissions.Report
	var grantsErr error
	if runtime.GOOS == "darwin" {
		var report permissions.Report
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		report, grantsErr = a.daemonPermissions(callCtx)
		cancel()
		grants = &report
	}
	if hasFlag(args, "--json") {
		report := map[string]any{"ok": failures == 0 && len(serviceProblems) == 0, "surfaces": results, "daemon": map[string]any{"supervisor": service.Name(), "problems": serviceProblems}, "claudeConfigDirs": claudeRoots, "claudePromptHook": promptHook, "claudeConfigRoots": claudeConfigRoots, "signin": signins}
		if claudeRootsErr != nil {
			report["claudeConfigError"] = claudeRootsErr.Error()
		}
		if grantsErr != nil {
			report["permissionsError"] = grantsErr.Error()
		} else if grants != nil {
			report["permissions"] = grants
		}
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		if claudeRootsErr != nil {
			fmt.Printf("claude config: %s\n", claudeRootsErr)
		}
		for i, root := range claudeRoots {
			label := "claude config"
			if i > 0 {
				label = "claude config (claudeConfigDirs)"
			}
			fmt.Printf("%s: %s\n", label, root)
			if described, ok := surface.ClaudeConfigRootFor(claudeConfigRoots, root); ok {
				fmt.Printf("  %s\n", describeClaudeConfigRoot(described, time.Now()))
			}
			switch promptHook[root] {
			case claudePromptHookMissing:
				fmt.Printf("  prompt hook: missing; terminal prompts can't be answered from Agenthail until you run: agenthail hooks install\n")
			case claudePromptHookOutdated:
				fmt.Printf("  prompt hook: outdated; terminal prompts may not reach Agenthail until you run: agenthail hooks install\n")
			}
		}
		if len(serviceProblems) > 0 {
			fmt.Printf("[daemon] supervisor: %s\n", service.Name())
			for _, problem := range serviceProblems {
				fmt.Printf("  problem: %s\n", problem.Detail)
				fmt.Printf("  fix: %s\n", problem.Fix)
			}
		}
		if grantsErr != nil {
			fmt.Printf("[permissions] unknown: %s\n", grantsErr)
		} else if grants != nil {
			printPermissions(*grants)
		}
		for _, status := range signins {
			fmt.Printf("[sign-in] %s: %s\n", status.ID, describeSignin(status))
			if status.Problem != "" {
				fmt.Printf("  problem: %s\n", status.Problem)
			}
			fmt.Printf("  change: agenthail signin use %s <profile|auto|none>, or Settings > Browser Sign-in\n", status.ID)
		}
		for _, result := range results {
			fmt.Printf("[%s] capabilities: %s\n", result.Surface, strings.Join(result.Capabilities, ", "))
			if result.Runtime != nil {
				state := "unavailable"
				if result.Runtime.Reachable && result.Runtime.Durable {
					state = "reachable and durable"
				} else if result.Runtime.Reachable {
					state = "reachable but unsupervised"
				}
				fmt.Printf("  runtime: %s", state)
				if result.Runtime.Backend != "" {
					fmt.Printf(" via %s", result.Runtime.Backend)
				}
				if result.Runtime.Detail != "" {
					fmt.Printf(" (%s)", result.Runtime.Detail)
				}
				fmt.Println()
				if result.Runtime.Remediation != "" {
					fmt.Printf("  fix: %s\n", result.Runtime.Remediation)
				}
				for _, note := range result.Runtime.Notes {
					fmt.Printf("  note: %s\n", note.Message)
					if note.Remediation != "" {
						fmt.Printf("    fix: %s\n", note.Remediation)
					}
				}
			}
			if result.SigninOff != "" {
				fmt.Printf("  sign-in off: %s\n", result.SigninOff)
			}
			if result.OK {
				fmt.Printf("  sessions: %d\n", result.Sessions)
				fmt.Printf("  writes: %s\n", result.WritePath)
			} else {
				fmt.Printf("  list: ERR %s\n", result.Error)
			}
		}
	}
	if len(serviceProblems) > 0 {
		return fmt.Errorf("doctor found %d unhealthy surface(s) and %d daemon problem(s)", failures, len(serviceProblems))
	}
	if failures > 0 {
		return fmt.Errorf("doctor found %d unhealthy surface(s)", failures)
	}
	return nil
}

func (a *App) isDaemonServiceLoaded() bool {
	return a.service().Loaded()
}

func (a *App) cmdLaunch(args []string) error {
	positional := stripFlags(args)
	if len(positional) > 1 {
		return fmt.Errorf("usage: agenthail launch [codex]")
	}
	target := "codex"
	if len(positional) > 0 {
		target = positional[0]
	}
	switch target {
	case "codex":
		return launchCodex(a.adapters().ForSurface(surface.KindCodex))
	case "claude":
		return fmt.Errorf("claude must be launched manually (open the app or visit claude.ai/code)")
	case "notion":
		return fmt.Errorf("notion must be launched manually (open app.notion.com in your browser and log in)")
	default:
		return fmt.Errorf("unknown surface '%s' (try: codex)", target)
	}
}

func launchCodex(codex surface.Surface) error {
	if codex == nil {
		return fmt.Errorf("Codex surface is unavailable")
	}
	install := surfaces.DetectCodexInstallation()
	exe := install.Desktop
	if exe == "" {
		message := fmt.Sprintf("Codex Desktop is not installed (tried %s)", strings.Join(surfaces.CodexDesktopExecutables, ", "))
		if install.Daemon != "" {
			message += "; Agenthail creates and drives Codex conversations on Codex's app-server daemon without it"
		}
		return errors.New(message)
	}

	pid := findCodexPID()
	if pid > 0 {
		if err := probeCodexDesktop(codex); err == nil {
			fmt.Println("Codex is ready for Agenthail")
			return nil
		}
		return fmt.Errorf("Codex is already open without Agenthail's Desktop bridge; quit Codex, then run 'agenthail launch codex'")
	}

	cmd := exec.Command(exe, codexLaunchArgs()...)
	cmd.Env = surface.AgentEnv(os.Environ())
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch codex: %w", err)
	}
	pid = cmd.Process.Pid
	cmd.Process.Release()
	fmt.Println("Opening Codex and waiting for it to connect")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := probeCodexDesktop(codex); err == nil {
			fmt.Println("Codex is ready for Agenthail")
			fmt.Println("Run 'agenthail doctor' to check every connection")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("Codex opened but its Desktop bridge did not become ready; run 'agenthail doctor' for details")
}

const codexLaunchProbeTimeout = 5 * time.Second

func probeCodexAppServer(codex surface.Surface) error {
	ctx, cancel := context.WithTimeout(context.Background(), codexLaunchProbeTimeout)
	defer cancel()
	if checker, ok := codex.(surface.ReadinessChecker); ok {
		return checker.Ready(ctx)
	}
	_, err := codex.List(ctx)
	return err
}

func probeCodexDesktop(codex surface.Surface) error {
	ctx, cancel := context.WithTimeout(context.Background(), codexLaunchProbeTimeout)
	defer cancel()
	if checker, ok := codex.(interface{ DesktopReady(context.Context) error }); ok {
		return checker.DesktopReady(ctx)
	}
	return probeCodexAppServer(codex)
}

func codexLaunchArgs() []string {
	port := codexconfig.LaunchRemoteDebuggingPort()
	return []string{
		"--no-first-run",
		"--no-default-browser-check",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + port,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func findCodexPID() int {
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return 0
	}
	return selectCodexPID(string(output), surfaces.CodexDesktopExecutables)
}

func selectCodexPID(output string, expectedExecutables []string) int {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
		for _, expectedExecutable := range expectedExecutables {
			if command == expectedExecutable || strings.HasPrefix(command, expectedExecutable+" ") {
				return pid
			}
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

// messageText returns text, or the contents of stdin when text is "-".
func messageText(text string) (string, error) {
	if text != "-" {
		return text, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}
