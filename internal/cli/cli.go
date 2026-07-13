package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type SurfaceEntry struct {
	Name    string
	Surface surface.Surface
}

type App struct {
	Registry            *registry.Registry
	Surfaces            []SurfaceEntry
	DefaultTimeout      time.Duration
	Version             string
	Revision            string
	BuiltAt             string
	daemonServiceLoaded func() bool
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		a.usage()
		return nil
	}
	cmd := args[0]
	rest := args[1:]
	if cmd == "codex" {
		return a.cmdCodex(rest)
	}
	if err := validateCommandFlags(cmd, rest); err != nil {
		return err
	}

	switch cmd {
	case "list", "ls":
		return a.cmdList(rest)
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
	case "interrupt":
		return a.cmdInterrupt(rest)
	case "steer":
		return a.cmdSteer(rest)
	case "queue":
		return a.cmdQueue(rest)
	case "history":
		return a.cmdHistory(rest)
	case "identify":
		return a.cmdIdentify(rest)
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
	case "dashboard":
		return a.cmdDashboard(rest)
	case "version", "--version":
		return a.cmdVersion(rest)
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
  list [--all]                   List active sessions (default 15, sorted by recency)
  send <target> "msg"|-       Send (--from, --model, --stream, --reply, --json, --timeout, --no-queue; - reads stdin)
  stream <target>               Tail live activity
  reply <target> [--json]       Fetch last assistant reply
  last <target> [count] [--full] [--json]  Show last N exchanges (full text with --full)
  goal <target> [text|clear]    Set or clear a goal
  compact <target>              Compress context
  model <target> [name]         Get or set model
  interrupt <target>            Stop current turn
  steer <target> "message"      Inject guidance into the running turn
  queue <target> "msg"|-        Hold until target is idle, then deliver (daemon required)
  queue list [--json] [--all]   Inspect pending, inflight, and dead-letter messages
  queue retry <id>              Retry a dead-letter message
  queue rm <id>                 Cancel a pending queued message
  queue clear <target>          Cancel all pending messages for a target
  history [target] [count]       Show durable delivery history (default 50)

Identity:
  identify <target> <name>      Name a session (henceforth @name resolves to it)
  identify rm <name>             Remove an alias
  identify list                 Show all names

Channels:
  channel create <name>         Create a channel
  channel add <name> <target>   Add a session to a channel
  channel rm <name> <target>   Remove a session from a channel (--all deletes)
  channel list                  List channels + members (shows @alias names)
  channel send <name> "msg"     Broadcast to all members (--from <name>)

Routing (auto-relay):
  relay add <from> <to> [regex] Send-to-on-completion rule
  relay list                    Show routing rules
  relay rm <id>                 Remove a rule

Daemon:
  daemon start                  Start the background daemon (auto-relay + steer)
  daemon stop                   Stop the daemon
  daemon restart                Restart manual or supervised daemon
  daemon status                 Is the daemon running?
  daemon install                Install/start a supervised macOS launchd service
  daemon uninstall              Remove the macOS launchd service

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
  version [--json]              Build and revision information

Targets: @name, PID, session id prefix, cwd/name fragment, or surface:target.
`)
}

func (a *App) cmdCodex(args []string) error {
	for _, arg := range args {
		if arg == "--remote" || strings.HasPrefix(arg, "--remote=") || arg == "--remote-auth-token-env" || strings.HasPrefix(arg, "--remote-auth-token-env=") {
			return fmt.Errorf("agenthail codex manages the remote transport; remove %s", arg)
		}
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("find codex: %w", err)
	}
	start := exec.Command(path, "app-server", "daemon", "start")
	start.Stdin = os.Stdin
	start.Stdout = io.Discard
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}
	argv := append([]string{"codex", "--remote", "unix://"}, args...)
	return syscall.Exec(path, argv, os.Environ())
}

func (a *App) cmdVersion(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail version [--json]")
	}
	result := map[string]any{"version": "dev", "revision": "unknown", "modified": false}
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

func (a *App) surfaceByKind(kind surface.SurfaceKind) surface.Surface {
	for _, entry := range a.Surfaces {
		if entry.Surface.Name() == kind {
			return entry.Surface
		}
	}
	return nil
}

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
	valueFlags := map[string]bool{"--from": true, "--model": true, "--timeout": true, "--codex-recent-hours": true, "--tailscale": true}
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
		"list": {bools: map[string]bool{"--all": true, "--json": true}}, "ls": {bools: map[string]bool{"--all": true, "--json": true}},
		"send":  {values: map[string]bool{"--from": true, "--model": true, "--timeout": true}, bools: map[string]bool{"--stream": true, "--reply": true, "--json": true, "--no-queue": true}},
		"reply": {bools: map[string]bool{"--json": true}}, "last": {bools: map[string]bool{"--full": true, "--json": true}}, "tail": {bools: map[string]bool{"--full": true, "--json": true}},
		"goal": {bools: map[string]bool{"--json": true}}, "queue": {}, "history": {bools: map[string]bool{"--json": true}},
		"channel": {},
		"doctor":  {bools: map[string]bool{"--json": true}}, "version": {bools: map[string]bool{"--json": true}}, "--version": {bools: map[string]bool{"--json": true}},
		"stream": {values: map[string]bool{"--timeout": true}}, "compact": {}, "model": {}, "interrupt": {}, "steer": {}, "identify": {}, "relay": {}, "daemon": {}, "daemon-run": {}, "launch": {}, "dashboard": {values: map[string]bool{"--codex-recent-hours": true, "--tailscale": true}, bools: map[string]bool{"--no-open": true, "--json": true}}, "help": {}, "-h": {}, "--help": {},
	}
	spec, known := specs[command]
	if !known {
		return nil
	}
	if command == "queue" && len(args) > 0 && args[0] == "list" {
		spec.bools = map[string]bool{"--all": true, "--json": true}
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

func (a *App) cmdList(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail list [--all] [--json]")
	}
	jsonOut := hasFlag(args, "--json")
	ctx := context.Background()

	allSessions := make([]surface.Session, 0)
	surfaceErrors := map[string]string{}
	for _, s := range a.allSurfaces() {
		sessions, err := s.List(ctx)
		if err != nil {
			surfaceErrors[string(s.Name())] = err.Error()
			continue
		}
		for _, sess := range sessions {
			if a.Registry != nil {
				if err := a.Registry.RegisterSession(sess); err != nil {
					surfaceErrors[string(s.Name())] = fmt.Sprintf("register session: %s", err)
					continue
				}
			}
			allSessions = append(allSessions, sess)
		}
	}

	aliased := make(map[string]string)
	if a.Registry != nil {
		rows, _ := a.Registry.ListAliases()
		for _, r := range rows {
			aliased[r.SessionID] = r.Name
		}
	}

	showAll := hasFlag(args, "--all")
	if !showAll {
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
	max := 15
	if showAll {
		max = len(allSessions)
	}
	if len(allSessions) > max {
		allSessions = allSessions[:max]
	}
	if jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"sessions": allSessions, "errors": surfaceErrors}); err != nil {
			return err
		}
		if len(surfaceErrors) > 0 {
			return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
		}
		return nil
	}
	for name, message := range surfaceErrors {
		fmt.Fprintf(os.Stderr, "warning: %s discovery failed: %s\n", name, message)
	}
	if len(allSessions) == 0 {
		fmt.Println("no sessions found")
		if len(surfaceErrors) > 0 {
			return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
		}
		return nil
	}

	fmt.Printf("%-7s %-5s %-14s %-28s %-20s %s\n", "SURFACE", "STAT", "AGENT", "SESSION", "PROJECT", "LAST")
	fmt.Printf("%-7s %-5s %-14s %-28s %-20s %s\n", "-------", "-----", "--------------", "----------------------------", "--------------------", "----------")
	queueCounts := map[string]int{}
	if a.Registry != nil {
		var err error
		queueCounts, err = a.Registry.QueueCounts()
		if err != nil {
			return fmt.Errorf("read queue counts: %w", err)
		}
	}
	for _, s := range allSessions {
		stat := sessStat(s, queueCounts[s.ID])
		agent := ""
		if alias, ok := aliased[s.ID]; ok {
			agent = "@" + alias
		}
		project := filepath.Base(s.Cwd)
		if project == "." {
			project = "-"
		}
		last := relTime(s.LastActive)
		fmt.Printf("%-7s %-5s %-14s %-28s %-20s %s\n",
			s.Surface, stat, truncate(agent, 14), truncate(s.Name, 28), truncate(project, 20), last)
	}
	if len(surfaceErrors) > 0 {
		return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
	}
	return nil
}

func sessStat(s surface.Session, queued int) string {
	busy := s.Status == surface.StatusBusy || s.Status == "running" || s.Status == "active"
	if busy {
		if queued > 0 {
			return "run+q"
		}
		return "run"
	}
	if queued > 0 {
		return "queue"
	}
	if s.Status == "idle" || s.Status == "shell" || s.Status == "notLoaded" || s.Status == surface.StatusIdle {
		return "idle"
	}
	if s.Status == "" || s.Status == surface.StatusUnknown {
		return "?"
	}
	return "idle"
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

func (a *App) resolveTarget(ctx context.Context, target string) (*surface.Session, surface.Surface, error) {
	if kindText, selector, ok := strings.Cut(target, ":"); ok {
		kind := surface.SurfaceKind(strings.ToLower(kindText))
		adapter := a.surfaceByKind(kind)
		if adapter == nil {
			return nil, nil, fmt.Errorf("unknown surface %q", kindText)
		}
		session, err := adapter.Resolve(ctx, selector)
		if err != nil {
			return nil, nil, fmt.Errorf("%s target: %w", kind, err)
		}
		isSyntheticNotion := kind == surface.KindNotion && (session.ID == "new" || strings.HasPrefix(session.ID, "new:"))
		if a.Registry != nil && !isSyntheticNotion {
			if err := a.Registry.RegisterSession(*session); err != nil {
				return nil, nil, fmt.Errorf("register %s session: %w", kind, err)
			}
		}
		return session, adapter, nil
	}
	target = strings.TrimPrefix(target, "@")
	if a.Registry != nil {
		sid, err := a.Registry.ResolveTarget(target)
		if err == nil {
			kindText, _, _, lookupErr := a.Registry.GetSession(sid)
			if lookupErr == nil {
				adapter := a.surfaceByKind(surface.SurfaceKind(kindText))
				if adapter == nil {
					return nil, nil, fmt.Errorf("session %s has unknown surface %q", sid, kindText)
				}
				session, resolveErr := adapter.Resolve(ctx, sid)
				if resolveErr != nil {
					return nil, nil, fmt.Errorf("resolve registered %s session: %w", adapter.Name(), resolveErr)
				}
				return session, adapter, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
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
		if a.Registry != nil {
			if err := a.Registry.RegisterSession(*candidate.session); err != nil {
				return nil, nil, fmt.Errorf("register %s session: %w", candidate.adapter.Name(), err)
			}
		}
		return candidate.session, candidate.adapter, nil
	}
	if len(bridgeErrors) > 0 {
		return nil, nil, fmt.Errorf("%s\nno session matched '%s' (bridge may be down)", strings.Join(bridgeErrors, "; "), target)
	}
	return nil, nil, fmt.Errorf("no session matched '%s'", target)
}

func (a *App) cmdSend(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf(`usage: agenthail send <target> "message" [--from <name>] [--stream] [--reply] [--json]`)
	}
	target := positional[0]
	message := strings.Join(positional[1:], " ")
	if message == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		message = string(data)
	} else if len(message) > 8000 {
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

	sess, surf, err := a.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	if !surf.Capabilities().Send {
		return fmt.Errorf("%s does not support send", surf.Name())
	}

	if wantStream && !surf.Capabilities().Stream {
		return fmt.Errorf("%s does not support stream", surf.Name())
	}
	baseline := ""
	if wantReply {
		observation, observeErr := surf.Observe(ctx, sess)
		if observeErr != nil {
			return fmt.Errorf("establish reply cursor before send: %w", observeErr)
		}
		if observation != nil {
			baseline = observation.CompletedTurnID
		}
	}
	options := surface.SendOptions{Model: flagVal(args, "--model")}
	dispatcher := delivery.Dispatcher{Registry: a.Registry}
	var receipt *delivery.Receipt
	syntheticNotion := surf.Name() == surface.KindNotion && (sess.ID == "new" || strings.HasPrefix(sess.ID, "new:"))
	syntheticNotionName := ""
	if syntheticNotion {
		syntheticNotionName = sess.Name
	}
	if hasFlag(args, "--no-queue") || syntheticNotion {
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

	if receipt.Disposition == delivery.DispositionQueued {
		if _, ok := daemon.IsRunning(); !ok {
			fmt.Fprintf(os.Stderr, "warning: daemon is not running; queued message will not be delivered until you start it (agenthail daemon start)\n")
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(receipt)
		} else {
			fmt.Printf("target is active; queued for %s and will deliver when idle (use 'agenthail steer %s \"message\"' to affect the current turn)\n", a.resolveDisplay(sess.ID), target)
		}
		return nil
	}
	if syntheticNotion && receipt.TurnID != "" {
		sess.ID = receipt.TurnID
		sess.Name = syntheticNotionName
		receipt.SessionID = sess.ID
		if a.Registry != nil {
			if err := a.Registry.RegisterSession(*sess); err != nil {
				return fmt.Errorf("Notion thread %s was created but could not be registered: %w", sess.ID, err)
			}
			if syntheticNotionName != "" {
				if err := a.Registry.SetAlias(syntheticNotionName, sess.ID); err != nil {
					return fmt.Errorf("Notion thread %s was created but alias %q could not be registered: %w", sess.ID, syntheticNotionName, err)
				}
			}
		}
	}

	if wantStream {
		return surf.Stream(ctx, sess, receipt.TurnID, func(ev surface.StreamEvent) {
			if ev.Kind == "text" {
				fmt.Print(ev.Text)
			} else if ev.Kind == "tool_use" {
				fmt.Printf("  -> %s\n", ev.Text)
			} else if ev.Kind == "done" {
				fmt.Println()
			}
		}, timeout)
	}

	if wantReply {
		reply, err := waitForReply(ctx, surf, sess, baseline, receipt.TurnID, timeout)
		if err != nil {
			return err
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
	} else {
		fmt.Printf("sent (turn %s)\n", receipt.TurnID)
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

func (a *App) cmdReply(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail reply <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if !surf.Capabilities().Reply {
		return fmt.Errorf("%s does not support reply", surf.Name())
	}
	reply, err := surf.Reply(ctx, sess, 50)
	if err != nil {
		return err
	}
	if reply == nil {
		return fmt.Errorf("%s returned an empty reply result", surf.Name())
	}
	if reply.Error != "" {
		return fmt.Errorf("latest %s turn did not complete successfully: %s", surf.Name(), reply.Error)
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "text": reply.Text, "done": reply.Done})
	} else {
		fmt.Println(reply.Text)
	}
	return nil
}

func (a *App) cmdLast(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: agenthail last <target> [count] [--full]")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	n := 1
	if len(positional) > 1 {
		v, parseErr := strconv.Atoi(positional[1])
		if parseErr != nil || v < 1 || v > 50 {
			return fmt.Errorf("count must be an integer from 1 to 50")
		}
		n = v
	}
	exchanges, err := surf.Tail(ctx, sess, n)
	if err != nil {
		return err
	}
	if len(exchanges) == 0 {
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "exchanges": []surface.Exchange{}})
		}
		fmt.Println("(no conversation history)")
		return nil
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "exchanges": exchanges})
	}
	label := a.resolveDisplay(sess.ID)
	fmt.Printf("── %s ──\n", label)
	full := hasFlag(args, "--full")
	for _, ex := range exchanges {
		if ex.User != "" {
			if full {
				fmt.Printf("you  ▸ %s\n", ex.User)
			} else {
				fmt.Printf("you  ▸ %s\n", truncate(strings.ReplaceAll(ex.User, "\n", " "), 200))
			}
		}
		if ex.Assistant != "" {
			if full {
				fmt.Printf(" ai  ▸ %s\n", ex.Assistant)
			} else {
				fmt.Printf(" ai  ▸ %s\n", truncate(strings.ReplaceAll(ex.Assistant, "\n", " "), 200))
			}
		}
		fmt.Println()
	}
	return nil
}

func (a *App) cmdStream(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail stream <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if !surf.Capabilities().Stream {
		return fmt.Errorf("%s does not support stream", surf.Name())
	}
	timeout, err := commandTimeout(args, 10*time.Minute)
	if err != nil {
		return err
	}
	return surf.Stream(ctx, sess, "", func(ev surface.StreamEvent) {
		switch ev.Kind {
		case "text":
			fmt.Print(ev.Text)
		case "tool_use":
			fmt.Printf("  -> %s\n", ev.Text)
		case "done":
			fmt.Println()
		}
	}, timeout)
}

func (a *App) cmdGoal(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail goal <target> [text|clear]")
	}
	ctx := context.Background()
	target := positional[0]
	sess, surf, err := a.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	if !surf.Capabilities().Goal {
		return fmt.Errorf("%s does not support goal management", surf.Name())
	}
	if len(positional) == 1 {
		goal, getErr := surf.GoalGet(ctx, sess)
		if getErr != nil {
			return getErr
		}
		if hasFlag(args, "--json") {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "goal": goal})
		}
		if goal == nil || goal.Objective == "" {
			fmt.Println("(no active goal)")
			return nil
		}
		fmt.Printf("%s [%s]\n", goal.Objective, goal.Status)
		return nil
	}
	action := positional[1]
	if action == "clear" {
		return surf.GoalClear(ctx, sess)
	}
	text := strings.Join(positional[1:], " ")
	return surf.GoalSet(ctx, sess, text)
}

func (a *App) cmdCompact(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail compact <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if !surf.Capabilities().Compact {
		return fmt.Errorf("%s does not support compact", surf.Name())
	}
	if err := surf.Compact(ctx, sess); err != nil {
		return err
	}
	fmt.Printf("compacted %s:%s\n", surf.Name(), sess.Name)
	return nil
}

func (a *App) cmdModel(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: agenthail model <target> [name]")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if !surf.Capabilities().Model {
		return fmt.Errorf("%s does not support model switching", surf.Name())
	}
	name := ""
	if len(positional) > 1 {
		name = positional[1]
	}
	current, err := surf.Model(ctx, sess, name)
	if err != nil {
		return err
	}
	if current != "" {
		fmt.Println(current)
	}
	return nil
}

func (a *App) cmdInterrupt(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail interrupt <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if !surf.Capabilities().Interrupt {
		return fmt.Errorf("%s does not support interrupt", surf.Name())
	}
	return surf.Interrupt(ctx, sess)
}

func (a *App) cmdSteer(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail steer <target> \"message\"")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if !surf.Capabilities().Steer {
		return fmt.Errorf("%s does not support steer", surf.Name())
	}
	return surf.Steer(ctx, sess, strings.Join(positional[1:], " "))
}

func (a *App) cmdQueue(args []string) error {
	if a.Registry == nil {
		return fmt.Errorf("queue requires the registry")
	}
	if len(args) > 0 && args[0] == "list" {
		rows, err := a.Registry.ListQueue(hasFlag(args, "--all"))
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
			fmt.Printf("#%-4d %-9s attempts=%d target=%s %s\n", row.ID, row.Status, row.Attempts, a.resolveDisplay(row.SessionID), truncate(strings.ReplaceAll(row.Message, "\n", " "), 100))
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
		item, err := a.Registry.QueueItem(id)
		if err != nil {
			return err
		}
		session, err := a.Registry.Session(item.SessionID)
		if err != nil {
			return err
		}
		if surface.IsReadOnlySession(session) {
			return errors.New(surface.ReadOnlySessionReason(session))
		}
		if err := a.Registry.RetryMessage(id); err != nil {
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
		if err := a.Registry.CancelMessage(id); err != nil {
			return err
		}
		fmt.Printf("queue item #%d canceled\n", id)
		return nil
	}
	if len(args) > 0 && args[0] == "clear" {
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail queue clear <target>")
		}
		sess, _, err := a.resolveTarget(context.Background(), args[1])
		if err != nil {
			return err
		}
		count, err := a.Registry.CancelMessagesForSession(sess.ID)
		if err != nil {
			return err
		}
		fmt.Printf("canceled %d queued message(s) for %s\n", count, a.resolveDisplay(sess.ID))
		return nil
	}
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail queue <target> \"message\"")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if a.Registry == nil {
		return fmt.Errorf("queue requires the registry")
	}
	if surf.Name() == surface.KindNotion && (sess.ID == "new" || strings.HasPrefix(sess.ID, "new:")) {
		return fmt.Errorf("a new Notion thread cannot be queued before it has a persisted UUID; use 'agenthail send %s ...'", positional[0])
	}
	if surface.IsReadOnlySession(sess) {
		return errors.New(surface.ReadOnlySessionReason(sess))
	}
	message := strings.Join(positional[1:], " ")
	if message == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		message = string(data)
	} else if len(message) > 8000 {
		fmt.Fprintf(os.Stderr, "warning: message is %d chars; shell may have truncated it. Use '-' to read from stdin for long messages.\n", len(message))
	}
	if message == "" {
		return fmt.Errorf("message is empty")
	}
	if _, ok := daemon.IsRunning(); !ok {
		fmt.Fprintf(os.Stderr, "warning: daemon is not running; queued message will not be delivered until you start it (agenthail daemon start)\n")
	}
	if err := a.Registry.QueueMessage(sess.ID, message); err != nil {
		return err
	}
	fmt.Printf("queued for %s (delivered when the target is idle on a daemon scan)\n", a.resolveDisplay(sess.ID))
	return nil
}

func (a *App) cmdHistory(args []string) error {
	if a.Registry == nil {
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
		if sessionID != "" {
			resolved, err := a.Registry.ResolveTarget(sessionID)
			if err != nil {
				return fmt.Errorf("resolve history target %q: %w", sessionID, err)
			}
			sessionID = resolved
		}
	}
	if limit > 200 {
		return fmt.Errorf("count must be 200 or less")
	}
	entries, err := a.Registry.ListHistory(limit, sessionID)
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
			fmt.Printf("%s %-9s %s -> %s", entry.CreatedAt, entry.Kind, a.resolveDisplay(entry.SourceSessionID), target)
		} else {
			fmt.Printf("%s %-9s %s", entry.CreatedAt, entry.Kind, target)
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

func (a *App) cmdDoctor(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail doctor [--json]")
	}
	ctx := context.Background()
	type doctorResult struct {
		Surface      string                 `json:"surface"`
		Capabilities []string               `json:"capabilities"`
		Sessions     int                    `json:"sessions"`
		OK           bool                   `json:"ok"`
		Error        string                 `json:"error,omitempty"`
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
		if healthErr == nil {
			sessions, err = e.Surface.List(ctx)
		} else {
			err = healthErr
		}
		result := doctorResult{Surface: e.Name, Capabilities: enabled, Sessions: len(sessions), OK: err == nil}
		if provider, ok := e.Surface.(surface.RuntimeStatusProvider); ok {
			runtimeStatus := provider.RuntimeStatus(ctx)
			if runtimeStatus.Name != "" {
				if runtimeStatus.Reachable && !runtimeStatus.Durable && runtimeStatus.Backend == "pid" && a.isDaemonServiceLoaded() {
					runtimeStatus.Durable = true
					runtimeStatus.Detail = "supervised by Agenthail across reboot"
					runtimeStatus.Remediation = ""
				}
				result.Runtime = &runtimeStatus
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
	if hasFlag(args, "--json") {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": failures == 0, "surfaces": results}); err != nil {
			return err
		}
	} else {
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
			}
			if result.OK {
				fmt.Printf("  sessions: %d\n", result.Sessions)
			} else {
				fmt.Printf("  list: ERR %s\n", result.Error)
			}
		}
	}
	if failures > 0 {
		return fmt.Errorf("doctor found %d unhealthy surface(s)", failures)
	}
	return nil
}

func (a *App) isDaemonServiceLoaded() bool {
	if a.daemonServiceLoaded != nil {
		return a.daemonServiceLoaded()
	}
	return daemonServiceLoaded()
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
		return launchCodex(codexRemotePort())
	case "claude":
		return fmt.Errorf("claude must be launched manually (open the app or visit claude.ai/code)")
	case "notion":
		return fmt.Errorf("notion must be launched manually (open app.notion.com in your browser and log in)")
	default:
		return fmt.Errorf("unknown surface '%s' (try: codex)", target)
	}
}

func launchCodex(port string) error {
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("AGENTHAIL_CODEX_REMOTE must be a TCP port from 1 to 65535 (got %q)", port)
	}
	candidates := []string{
		"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT",
		"/Applications/Codex.app/Contents/MacOS/Codex",
	}
	var exe string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			exe = c
			break
		}
	}
	if exe == "" {
		return fmt.Errorf("codex binary not found (tried %s)", strings.Join(candidates, ", "))
	}

	pid := findCodexPID()
	if pid > 0 {
		if rendererDebuggerListening(port) {
			fmt.Printf("Codex already running (pid %d, renderer bridge ready on 127.0.0.1:%s)\n", pid, port)
			return nil
		}
		if nodeDebuggerListening("9229") {
			fmt.Printf("Codex already running (pid %d); connected through the delayed compatibility bridge on 127.0.0.1:9229\n", pid)
			fmt.Println("for the safer renderer-only path, quit Codex once and run 'agenthail launch codex'")
			return nil
		}
		if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
			return fmt.Errorf("Codex is already running without a renderer debugger, and compatibility activation failed (pid %d): %w; quit it, then run 'agenthail launch codex'", pid, err)
		}
		time.Sleep(1 * time.Second)
		if nodeDebuggerListening("9229") {
			fmt.Printf("connected to existing Codex (pid %d) through the delayed compatibility bridge on 127.0.0.1:9229\n", pid)
			fmt.Println("for the safer renderer-only path, quit Codex once and run 'agenthail launch codex'")
			return nil
		}
		return fmt.Errorf("Codex is already running but neither renderer nor compatibility debugging is available (pid %d); quit it, then run 'agenthail launch codex'", pid)
	}

	cmd := exec.Command(exe, codexLaunchArgs(port)...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch codex: %w", err)
	}
	pid = cmd.Process.Pid
	cmd.Process.Release()
	fmt.Printf("launched Codex (pid %d), waiting for startup...\n", pid)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if rendererDebuggerListening(port) {
			fmt.Printf("renderer bridge ready on 127.0.0.1:%s (pid %d)\nrun 'agenthail doctor' to verify\n", port, pid)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("Codex launched (pid %d) but renderer bridge did not become ready on 127.0.0.1:%s", pid, port)
}

func codexLaunchArgs(port string) []string {
	return []string{
		"--no-first-run",
		"--no-default-browser-check",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + port,
	}
}

func codexRemotePort() string {
	if value := os.Getenv("AGENTHAIL_CODEX_REMOTE"); value != "" {
		return value
	}
	if value := os.Getenv("AGENTHAIL_CODEX_INSPECT"); value != "" {
		return value
	}
	return "9231"
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
	return selectCodexPID(string(output), []string{
		"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT",
		"/Applications/Codex.app/Contents/MacOS/Codex",
	})
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

func rendererDebuggerListening(port string) bool {
	targets, err := codexDebugTargets(port)
	if err != nil {
		return false
	}
	for _, target := range targets {
		targetType, _ := target["type"].(string)
		targetURL, _ := target["url"].(string)
		if (targetType == "page" || targetType == "window") && strings.HasPrefix(targetURL, "app://") {
			return true
		}
	}
	return false
}

func nodeDebuggerListening(port string) bool {
	targets, err := codexDebugTargets(port)
	if err != nil {
		return false
	}
	for _, target := range targets {
		if target["type"] == "node" {
			return true
		}
	}
	return false
}

func codexDebugTargets(port string) ([]map[string]any, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("debug endpoint returned HTTP %d", resp.StatusCode)
	}
	var targets []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
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
