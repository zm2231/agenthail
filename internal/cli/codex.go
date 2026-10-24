package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

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
