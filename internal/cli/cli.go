package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type SurfaceEntry struct {
	Name    string
	Surface surface.Surface
}

type App struct {
	Registry                  *registry.Registry
	OpenRegistry              func(writeDeadline context.Context) (*registry.Registry, error)
	OpenCurrentRegistry       func(writeDeadline context.Context) (*registry.Registry, error)
	registryMu                sync.Mutex
	registryErr               error
	writeDeadline             context.Context
	releaseWrites             func()
	Surfaces                  []SurfaceEntry
	UIDir                     string
	uiAuthorName              string
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
	scopeOverride             func(daemon.ScopeRequest) (*daemon.ScopeReply, error)
	askPerson                 bool
	askReason                 string
	agentNotices              []string
	deadlineClock             func() time.Time
}

type registryUnavailable struct{ err error }

func (a *App) registry() *registry.Registry {
	r, err := a.openRegistry()
	if err != nil {
		panic(registryUnavailable{err})
	}
	return r
}

// OpenedRegistry is the registry, opened on first use, or nil when it cannot be opened.
func (a *App) OpenedRegistry() *registry.Registry {
	r, _ := a.openRegistry()
	return r
}

func (a *App) openRegistry() (*registry.Registry, error) {
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	if a.Registry != nil || a.OpenRegistry == nil || a.registryErr != nil {
		return a.Registry, a.registryErr
	}
	deadline := a.writeDeadline
	if deadline == nil {
		deadline = context.Background()
	}
	a.Registry, a.registryErr = a.OpenRegistry(deadline)
	if a.registryErr != nil {
		a.registryErr = fmt.Errorf("open registry: %w", a.registryErr)
	} else {
		a.Registry.SetBusyPolicy(deliverypolicy.Current)
		a.registryErr = a.bindWritesLocked()
	}
	return a.Registry, a.registryErr
}

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

func (a *App) SigninCandidates(consumer signin.Consumer) ([]signin.Source, error) {
	r, err := a.openRegistry()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("the registry is not available")
	}
	return r.SigninCandidates(consumer)
}

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
	var rest []string
	rest, a.askPerson, a.askReason = askPersonFlags(args[1:])
	defer func() {
		if err == nil {
			a.sendAgentNotices()
		}
	}()
	defer func() {
		var asked *askedPerson
		if errors.As(err, &asked) {
			fmt.Printf("Asked the person to allow this (%s). Their answer, and the result if they allow it, arrives as your next message.\n", asked.askID)
			err = nil
		}
	}()
	if cmd == "codex" {
		return a.cmdCodex(rest)
	}
	if cmd == "launcher-exec" {
		return a.cmdLauncherExec(rest)
	}
	if spec, known := commandFlagSpec(cmd, rest); known && !spec.bools["--help"] && (len(rest) > 0 && rest[0] == "-h" || hasFlag(rest, "--help")) {
		a.commandHelp(cmd, rest)
		return nil
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
	case "usage":
		return a.cmdUsage(rest)
	case "proxies":
		return a.cmdProxies(rest)
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
	case "ask":
		return a.cmdAsk(rest)
	case "brief":
		return a.cmdBrief(rest)
	case "control":
		return a.cmdControl(rest)
	case "interrupt":
		return a.cmdInterrupt(rest)
	case "steer":
		return a.cmdSteer(rest)
	case "queue":
		return a.cmdQueue(rest)
	case "limit-resume":
		return a.cmdLimitResume(rest)
	case "agent-policy":
		return a.cmdAgentPolicy(rest)
	case "history":
		return a.cmdHistory(rest)
	case "thread":
		return a.cmdThread(rest)
	case "pr":
		return a.cmdPR(rest)
	case "hosts":
		return a.cmdHosts(rest)
	case "move":
		return a.cmdMove(rest)
	case "devices":
		return a.cmdDevices(rest)
	case "project", "projects":
		return a.cmdProject(rest)
	case "signin":
		return a.cmdSignin(rest)
	case "identify":
		return a.cmdIdentify(rest)
	case "identity", "identities":
		return a.cmdIdentity(rest)
	case "inbox":
		return a.cmdInbox(rest)
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
	case "debug":
		return a.cmdDebug(rest)
	case "mcp":
		return a.cmdMCP(rest)
	case "launch":
		return a.cmdLaunch(rest)
	case "doctor":
		return a.cmdDoctor(rest)
	case "registry":
		return a.cmdRegistry(rest)
	case "hook":
		return a.cmdHook(rest)
	case "hooks":
		return a.cmdHooks(rest)
	case "triggers":
		return a.cmdTriggers(rest)
	case "extensions":
		return a.cmdExtensions(rest)
	case "permissions":
		return a.cmdPermissions(rest)
	case "person":
		return a.cmdPerson(rest)
	case "ledger":
		return a.cmdLedger(rest)
	case "ui":
		return a.cmdUI(rest)
	case "dashboard":
		return a.cmdDashboard(rest)
	case "version", "--version":
		return a.cmdVersion(rest)
	case "update", "upgrade":
		return a.cmdUpdate(rest)
	case "help", "-h", "--help":
		if len(rest) > 0 && rest[0] == "agent" {
			return a.cmdHelpAgent(rest[1:])
		}
		if len(rest) > 0 && rest[0] == "ledger" {
			fmt.Print(ledgerHelp)
			return nil
		}
		if len(rest) > 0 {
			a.commandHelp(rest[0], rest[1:])
			return nil
		}
		a.usage()
		return nil
	default:
		return fmt.Errorf("unknown command '%s' (try 'help')", cmd)
	}
}

func askPersonFlags(args []string) ([]string, bool, string) {
	if !slices.Contains(args, "--ask-person") {
		return args, false, ""
	}
	rest := make([]string, 0, len(args))
	ask, reason := false, ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			return append(rest, args[i:]...), ask, reason
		case args[i] == "--ask-person":
			ask = true
		case args[i] == "--reason" && i+1 < len(args):
			reason = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--reason="):
			reason = strings.TrimPrefix(args[i], "--reason=")
		default:
			rest = append(rest, args[i])
		}
	}
	return rest, ask, reason
}
