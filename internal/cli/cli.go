package cli

import (
	"errors"
	"fmt"
	"sync"
	"time"

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
	OpenRegistry              func() (*registry.Registry, error)
	OpenCurrentRegistry       func() (*registry.Registry, error)
	registryMu                sync.Mutex
	registryErr               error
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
}

type registryUnavailable struct{ err error }

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
	case "history":
		return a.cmdHistory(rest)
	case "thread":
		return a.cmdThread(rest)
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
	case "person":
		return a.cmdPerson(rest)
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
		a.usage()
		return nil
	default:
		return fmt.Errorf("unknown command '%s' (try 'help')", cmd)
	}
}
