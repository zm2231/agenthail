package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/permissions"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func (a *App) cmdDoctor(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail doctor [--json]")
	}
	ctx := context.Background()
	type doctorResult struct {
		Surface           string                 `json:"surface"`
		Capabilities      []string               `json:"capabilities"`
		Sessions          int                    `json:"sessions"`
		WritePath         string                 `json:"writePath"`
		OK                bool                   `json:"ok"`
		Error             string                 `json:"error,omitempty"`
		SigninOff         string                 `json:"signinOff,omitempty"`
		SigninUnavailable string                 `json:"signinUnavailable,omitempty"`
		Runtime           *surface.RuntimeStatus `json:"runtime,omitempty"`
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
		signinOff, signinUnavailable := "", ""
		if signin.IsOff(healthErr) {
			signinOff = healthErr.Error()
			healthErr = nil
		}
		if signin.IsUnavailable(healthErr) {
			signinUnavailable = healthErr.Error()
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
		if signin.IsUnavailable(err) {
			signinUnavailable = err.Error()
			err = nil
		}
		result := doctorResult{Surface: e.Name, Capabilities: enabled, Sessions: len(sessions), WritePath: "checked immediately before delivery", OK: err == nil, SigninOff: signinOff, SigninUnavailable: signinUnavailable}
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
	claudeHookStates := map[string]string{}
	for _, root := range claudeRoots {
		state, err := claudeHooksState(filepath.Join(root, "settings.json"))
		if err != nil {
			state = claudeHookMissing
		}
		claudeHookStates[root] = state
	}
	remoteControl := map[string]string{}
	if a.claudeRemoteControlAvailable() {
		settings, _ := surface.ReadClaudeRemoteControl(home)
		for _, setting := range settings {
			remoteControl[setting.Root] = describeRemoteControlAtStartup(setting)
		}
	}
	codexHooks := ""
	if executable, err := os.Executable(); err == nil {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		codexHooks, _ = surfaces.CodexContextState(callCtx, executable)
		cancel()
	}
	claudeConfigRoots, _ := a.claudeConfigRoots(ctx)
	service := a.service()
	serviceProblems := service.Problems()
	if serviceProblems == nil {
		serviceProblems = []serviceProblem{}
	}
	build, buildProblem := a.checkDaemonBuild(ctx)
	if buildProblem != nil {
		serviceProblems = append(serviceProblems, *buildProblem)
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
	personName, personSet := "", false
	if r := a.registry(); r != nil {
		personName, _ = r.PersonName()
		setting, _ := r.PersonNameSetting()
		personSet = setting != ""
	}
	if hasFlag(args, "--json") {
		report := map[string]any{"ok": failures == 0 && len(serviceProblems) == 0, "person": map[string]any{"name": personName, "set": personSet}, "surfaces": results, "daemon": map[string]any{"supervisor": service.Name(), "problems": serviceProblems, "build": build}, "claudeConfigDirs": claudeRoots, "claudeHooks": claudeHookStates, "claudeRemoteControlAtStartup": remoteControl, "codexHooks": codexHooks, "claudeConfigRoots": claudeConfigRoots, "signin": signins}
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
		if personName != "" {
			fmt.Println(describePersonName(personName, personSet))
		}
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
			switch claudeHookStates[root] {
			case claudeHookMissing:
				fmt.Printf("  hooks: missing; terminal prompts can't be answered from Agenthail, and terminal sessions aren't told who they are, until you run: agenthail hooks install\n")
			case claudeHookOutdated:
				fmt.Printf("  hooks: outdated; terminal prompts may not reach Agenthail, and terminal sessions may not be told who they are, until you run: agenthail hooks install\n")
			}
			if state, ok := remoteControl[root]; ok {
				fmt.Printf("  remote control at startup: %s\n", state)
			}
		}
		switch codexHooks {
		case surfaces.CodexHooksMissing:
			fmt.Printf("codex hooks: missing; Codex sessions, including the ones Agenthail starts, aren't told who they are until you run: agenthail hooks install codex\n")
		case surfaces.CodexHooksOutdated:
			fmt.Printf("codex hooks: outdated; Codex sessions may not be told who they are until you run: agenthail hooks install codex\n")
		case surfaces.CodexHooksUntrusted:
			fmt.Printf("codex hooks: Codex hook not trusted yet: run agenthail hooks install codex or trust it in Codex\n")
		}
		if build != nil {
			fmt.Printf("[daemon] build: %s\n", build.Detail)
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
			if status.Unavailable != "" {
				fmt.Printf("  unavailable: %s\n", status.Unavailable)
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
			if result.SigninUnavailable != "" {
				fmt.Printf("  sign-in unavailable: %s\n", result.SigninUnavailable)
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
