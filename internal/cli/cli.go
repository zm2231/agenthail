package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type SurfaceEntry struct {
	Name    string
	Surface surface.Surface
}

type App struct {
	Registry       *registry.Registry
	Surfaces       []SurfaceEntry
	DefaultTimeout time.Duration
}

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		a.usage()
		return nil
	}
	cmd := args[0]
	rest := args[1:]

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
	case "help", "-h", "--help":
		a.usage()
		return nil
	default:
		return fmt.Errorf("unknown command '%s' (try 'help')", cmd)
	}
}

func (a *App) usage() {
	fmt.Print(`agenthail — hail an agent

Usage:
  agenthail <command> [target] [args] [options]

Session commands:
  list [--all]                   List active sessions (default 15, sorted by recency)
  send <target> "message"       Send a message (--from <name>, --stream, --reply, --json)
  stream <target>               Tail live activity
  reply <target>                Fetch last assistant reply
  last <target> [count]        Show last N exchanges (default 3)
  goal <target> [text|clear]    Set or clear a goal
  compact <target>              Compress context
  model <target> [name]         Get or set model
  interrupt <target>            Stop current turn
  steer <target> "message"      Inject guidance into the running turn
  queue <target> "message"      Hold until the current turn completes, then deliver

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
  daemon status                 Is the daemon running?

Other:
  launch <surface>              Launch a surface app with debug settings
  doctor                        Health check

Targets: @name, PID, session id prefix, or cwd/name fragment.
`)
}

func (a *App) allSurfaces() []surface.Surface {
	out := make([]surface.Surface, len(a.Surfaces))
	for i, e := range a.Surfaces {
		out[i] = e.Surface
	}
	return out
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// flagVal returns the value following a --flag, or "" if not present.
func flagVal(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// stripFlags removes -- flags and their values, returning only positional args.
// Flags that take values (--from, --model) are skipped along with their value.
func stripFlags(args []string) []string {
	valueFlags := map[string]bool{"--from": true, "--model": true}
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			if valueFlags[a] && i+1 < len(args) {
				i++ // skip the value
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func (a *App) cmdList(args []string) error {
	jsonOut := hasFlag(args, "--json")
	ctx := context.Background()

	var allSessions []surface.Session
	for _, s := range a.allSurfaces() {
		sessions, err := s.List(ctx)
		if err != nil {
			continue
		}
		for _, sess := range sessions {
			if a.Registry != nil {
				a.Registry.RegisterSession(sess)
			}
			allSessions = append(allSessions, sess)
			if jsonOut {
				b, _ := json.Marshal(sess)
				fmt.Println(string(b))
			}
		}
	}
	if jsonOut {
		if len(allSessions) == 0 {
			fmt.Println("[]")
		}
		return nil
	}
	if len(allSessions) == 0 {
		fmt.Println("no sessions found")
		return nil
	}

	// Build alias map (sessionID -> alias name)
	aliased := make(map[string]string)
	if a.Registry != nil {
		rows, _ := a.Registry.ListAliases()
		for _, r := range rows {
			aliased[r.SessionID] = r.Name
		}
	}

	// Filter: hide Notion threads older than 7 days unless --all
	if !hasFlag(args, "--all") {
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

	// Sort by last active (most recent first), sessions with zero time go last
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
	if hasFlag(args, "--all") {
		max = len(allSessions)
	}
	if len(allSessions) > max {
		allSessions = allSessions[:max]
	}

	fmt.Printf("%-7s %-4s %-14s %-28s %-20s %s\n", "SURFACE", "STAT", "AGENT", "SESSION", "PROJECT", "LAST")
	fmt.Printf("%-7s %-4s %-14s %-28s %-20s %s\n", "-------", "----", "--------------", "----------------------------", "--------------------", "----------")
	for _, s := range allSessions {
		stat := "○"
		if s.PID > 0 {
			stat = "●"
		}
		agent := ""
		if alias, ok := aliased[s.ID]; ok {
			agent = "@" + alias
		}
		project := filepath.Base(s.Cwd)
		if project == "." {
			project = "-"
		}
		last := relTime(s.LastActive)
		fmt.Printf("%-7s %-4s %-14s %-28s %-20s %s\n",
			s.Surface, stat, truncate(agent, 14), truncate(s.Name, 28), truncate(project, 20), last)
	}
	return nil
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
	target = strings.TrimPrefix(target, "@")
	if a.Registry != nil {
		if sid, err := a.Registry.ResolveTarget(target); err == nil {
			for _, s := range a.allSurfaces() {
				if sess, err := s.Resolve(ctx, sid); err == nil {
					return sess, s, nil
				}
			}
		}
	}
	for _, s := range a.allSurfaces() {
		if sess, err := s.Resolve(ctx, target); err == nil {
			return sess, s, nil
		}
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
	wantStream := hasFlag(args, "--stream")
	wantReply := hasFlag(args, "--reply")
	jsonOut := hasFlag(args, "--json")
	ctx := context.Background()

	// --from <name>: inject sender attribution into the message
	fromLabel := flagVal(args, "--from")
	if fromLabel != "" {
		message = fmt.Sprintf("[from %s] %s", fromLabel, message)
	}

	sess, surf, err := a.resolveTarget(ctx, target)
	if err != nil {
		return err
	}

	result, err := surf.Send(ctx, sess, message)
	if err != nil {
		return err
	}

	// Surface refused because a turn is already running (e.g. Codex). Auto-queue
	// so the message lands when the current turn completes, matching native
	// Codex submit-while-busy behavior. The daemon drains the queue.
	if !result.Accepted && a.Registry != nil {
		if err := a.Registry.QueueMessage(sess.ID, message); err != nil {
			return err
		}
		if jsonOut {
			fmt.Printf(`{"queued":true,"target":"%s"}`, sess.ID)
			fmt.Println()
		} else {
			fmt.Printf("queued for %s (busy; delivered on turn completion)\n", a.resolveDisplay(sess.ID))
		}
		return nil
	}

	if wantStream {
		return surf.Stream(ctx, sess, result.UUID, func(ev surface.StreamEvent) {
			if ev.Kind == "text" {
				fmt.Print(ev.Text)
				if !strings.HasSuffix(ev.Text, "\n") {
					fmt.Println()
				}
			} else if ev.Kind == "tool_use" {
				fmt.Printf("  -> %s\n", ev.Text)
			} else if ev.Kind == "done" {
				fmt.Println()
			}
		}, a.DefaultTimeout)
	}

	if wantReply {
		time.Sleep(2 * time.Second)
		reply, err := surf.Reply(ctx, sess, 50)
		if err == nil && reply.Text != "" {
			fmt.Println(reply.Text)
		}
	}

	if jsonOut {
		b, _ := json.Marshal(result)
		fmt.Println(string(b))
	} else {
		fmt.Printf("sent (uuid %s)\n", result.UUID)
	}
	return nil
}

func (a *App) cmdReply(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail reply <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	reply, err := surf.Reply(ctx, sess, 50)
	if err != nil {
		return err
	}
	fmt.Println(reply.Text)
	return nil
}

func (a *App) cmdLast(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail last <target> [count]")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	n := 3 // default: last 3 exchanges
	if len(positional) > 1 {
		if v, e := strconv.Atoi(positional[1]); e == nil && v > 0 && v <= 50 {
			n = v
		}
	}
	exchanges, err := surf.Tail(ctx, sess, n)
	if err != nil {
		return err
	}
	if len(exchanges) == 0 {
		fmt.Println("(no conversation history)")
		return nil
	}
	label := a.resolveDisplay(sess.ID)
	fmt.Printf("── %s ──\n", label)
	for _, ex := range exchanges {
		if ex.User != "" {
			fmt.Printf("you  ▸ %s\n", truncate(strings.ReplaceAll(ex.User, "\n", " "), 200))
		}
		if ex.Assistant != "" {
			fmt.Printf(" ai  ▸ %s\n", truncate(strings.ReplaceAll(ex.Assistant, "\n", " "), 200))
		}
		fmt.Println()
	}
	return nil
}

func (a *App) cmdStream(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail stream <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
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
	}, 10*time.Minute)
}

func (a *App) cmdGoal(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail goal <target> [text|clear]")
	}
	ctx := context.Background()
	target := positional[0]
	action := positional[1]
	sess, surf, err := a.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	if action == "clear" {
		return surf.GoalClear(ctx, sess)
	}
	text := strings.Join(positional[1:], " ")
	return surf.GoalSet(ctx, sess, text)
}

func (a *App) cmdCompact(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail compact <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	return surf.Compact(ctx, sess)
}

func (a *App) cmdModel(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail model <target> [name]")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
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
	if len(positional) < 1 {
		return fmt.Errorf("usage: agenthail interrupt <target>")
	}
	ctx := context.Background()
	sess, surf, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
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
	positional := stripFlags(args)
	if len(positional) < 2 {
		return fmt.Errorf("usage: agenthail queue <target> \"message\"")
	}
	ctx := context.Background()
	sess, _, err := a.resolveTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if a.Registry == nil {
		return fmt.Errorf("queue requires the registry")
	}
	message := strings.Join(positional[1:], " ")
	if err := a.Registry.QueueMessage(sess.ID, message); err != nil {
		return err
	}
	fmt.Printf("queued for %s (delivered on next turn completion by the daemon)\n", a.resolveDisplay(sess.ID))
	return nil
}

func (a *App) cmdDoctor(args []string) error {
	ctx := context.Background()
	for _, e := range a.Surfaces {
		caps := e.Surface.Capabilities()
		fmt.Printf("[%s] capabilities: ", e.Name)
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
		if caps.Fork {
			enabled = append(enabled, "fork")
		}
		fmt.Println(strings.Join(enabled, ", "))
		sessions, err := e.Surface.List(ctx)
		if err != nil {
			fmt.Printf("  list: ERR %s\n", err)
		} else {
			fmt.Printf("  sessions: %d\n", len(sessions))
		}
	}
	return nil
}

func (a *App) cmdLaunch(args []string) error {
	positional := stripFlags(args)
	target := "codex"
	if len(positional) > 0 {
		target = positional[0]
	}
	switch target {
	case "codex":
		return launchCodex()
	case "claude":
		return fmt.Errorf("claude must be launched manually (open the app or visit claude.ai/code)")
	case "notion":
		return fmt.Errorf("notion must be launched manually (open app.notion.com in your browser and log in)")
	default:
		return fmt.Errorf("unknown surface '%s' (try: codex)", target)
	}
}

func launchCodex() error {
	inspectorPort := envOr("AGENTHAIL_CODEX_INSPECT", "9230")
	remotePort := envOr("AGENTHAIL_CODEX_REMOTE", "9231")
	args := fmt.Sprintf("--inspect=127.0.0.1:%s --remote-debugging-port=%s", inspectorPort, remotePort)
	cmd := exec.Command("open", "-a", "Codex", "--args", args)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launch codex: %w", err)
	}
	fmt.Printf("launched Codex (inspect=127.0.0.1:%s, remote=%s)\nwait a few seconds for the app to start, then run 'agenthail list'\n", inspectorPort, remotePort)
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
