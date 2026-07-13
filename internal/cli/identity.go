package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) resolveDisplay(sessionID string) string {
	if a.Registry != nil {
		if alias, err := a.Registry.ReverseAlias(sessionID); err == nil && alias != "" {
			return "@" + alias
		}
		if sfc, name, _, err := a.Registry.GetSession(sessionID); err == nil {
			if name != "" {
				return fmt.Sprintf("%s/%s", sfc, truncate(name, 20))
			}
		}
	}
	return truncate(sessionID, 24)
}

func (a *App) cmdIdentify(args []string) error {
	if a.Registry == nil {
		return fmt.Errorf("registry not available")
	}
	if len(args) == 0 {
		return a.identifyList()
	}
	if args[0] == "list" {
		if len(args) != 1 {
			return fmt.Errorf("usage: agenthail identify list")
		}
		return a.identifyList()
	}
	if args[0] == "rm" || args[0] == "remove" || args[0] == "unidentify" {
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail identify rm <name>")
		}
		name := strings.TrimPrefix(args[1], "@")
		if err := a.Registry.RemoveAlias(name); err != nil {
			return err
		}
		fmt.Printf("removed @%s\n", name)
		return nil
	}
	if len(args) != 2 {
		return fmt.Errorf("usage: agenthail identify <target> <name>")
	}
	target := strings.TrimPrefix(args[0], "@")
	name := strings.TrimPrefix(args[1], "@")
	ctx := context.Background()
	sess, _, err := a.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	if err := a.Registry.SetAlias(name, sess.ID); err != nil {
		return err
	}
	fmt.Printf("@%s -> %s (%s)\n", name, truncate(sess.ID, 24), sess.Surface)
	return nil
}

func (a *App) identifyList() error {
	rows, err := a.Registry.ListAliases()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("(no names set; use: agenthail identify <target> <name>)")
		return nil
	}
	for _, r := range rows {
		label := a.resolveDisplay(r.SessionID)
		fmt.Printf("@%-20s %s\n", r.Name, label)
	}
	return nil
}

func (a *App) cmdChannel(args []string) error {
	if a.Registry == nil {
		return fmt.Errorf("registry not available")
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthail channel <create|add|rm|list|send> ...")
	}
	ctx := context.Background()
	switch args[0] {
	case "create":
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail channel create <name>")
		}
		id, err := a.Registry.CreateChannel(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("created channel #%s (id %s)\n", args[1], id)
		return nil
	case "add":
		if len(args) != 3 {
			return fmt.Errorf("usage: agenthail channel add <channel> <target>")
		}
		channelName := strings.TrimPrefix(args[1], "#")
		sess, _, err := a.resolveTarget(ctx, args[2])
		if err != nil {
			return err
		}
		if surface.IsReadOnlySession(sess) {
			return fmt.Errorf("%s", surface.ReadOnlySessionReason(sess))
		}
		if err := a.Registry.AddToChannel(channelName, sess.ID); err != nil {
			return err
		}
		fmt.Printf("added %s to #%s\n", a.resolveDisplay(sess.ID), channelName)
		return nil
	case "rm", "remove":
		if len(args) != 3 {
			return fmt.Errorf("usage: agenthail channel rm <channel> <target|--all>")
		}
		channelName := strings.TrimPrefix(args[1], "#")
		if args[2] == "--all" || args[2] == "-a" {
			if err := a.Registry.DeleteChannel(channelName); err != nil {
				return err
			}
			fmt.Printf("deleted channel #%s\n", channelName)
			return nil
		}
		sess, _, err := a.resolveTarget(ctx, args[2])
		if err != nil {
			return err
		}
		if err := a.Registry.RemoveFromChannel(channelName, sess.ID); err != nil {
			return err
		}
		fmt.Printf("removed %s from #%s\n", a.resolveDisplay(sess.ID), channelName)
		return nil
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: agenthail channel list")
		}
		channels, err := a.Registry.ListChannels()
		if err != nil {
			return err
		}
		if len(channels) == 0 {
			fmt.Println("(no channels; use: agenthail channel create <name>)")
			return nil
		}
		for _, ch := range channels {
			fmt.Printf("#%s  (%d members)\n", ch.Name, ch.MemberCount)
			for _, m := range ch.Members {
				fmt.Printf("    %s\n", a.resolveDisplay(m))
			}
		}
		return nil
	case "send", "broadcast":
		if len(args) < 3 {
			return fmt.Errorf("usage: agenthail channel send <channel> \"message\" [--from <name>]")
		}
		channelName := strings.TrimPrefix(args[1], "#")
		var fromLabel string
		var msgParts []string
		positionalOnly := false
		for i := 2; i < len(args); i++ {
			if args[i] == "--" {
				positionalOnly = true
				continue
			}
			if !positionalOnly && args[i] == "--from" && i+1 < len(args) {
				fromLabel = args[i+1]
				i++
				continue
			}
			msgParts = append(msgParts, args[i])
		}
		message := strings.Join(msgParts, " ")
		if message == "" {
			return fmt.Errorf("message is empty")
		}
		if fromLabel == "" {
			fromLabel = "hail"
		}
		payload := fmt.Sprintf("[from %s via #%s] %s", fromLabel, channelName, message)
		members, err := a.Registry.ChannelMembers(channelName)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			return fmt.Errorf("channel #%s has no members", channelName)
		}
		var sent, queued, failed int
		for _, mid := range members {
			sess, surf, err := a.resolveTarget(ctx, mid)
			if err != nil {
				failed++
				fmt.Printf("  [FAIL] %s: %s\n", a.resolveDisplay(mid), err)
				continue
			}
			receipt, err := (delivery.Dispatcher{Registry: a.Registry}).Deliver(ctx, surf, sess, payload, "")
			if err != nil {
				failed++
				fmt.Printf("  [FAIL] %s: %s\n", a.resolveDisplay(mid), err)
				continue
			}
			if receipt.Disposition == delivery.DispositionQueued {
				queued++
				fmt.Printf("  [QUEUE] %s\n", a.resolveDisplay(mid))
			} else {
				sent++
				fmt.Printf("  [ OK ] %s\n", a.resolveDisplay(mid))
			}
		}
		fmt.Printf("channel #%s: %d sent, %d queued, %d failed\n", channelName, sent, queued, failed)
		if queued > 0 {
			if _, running := daemon.IsRunning(); !running {
				fmt.Fprintln(os.Stderr, "warning: daemon is not running; queued channel deliveries will wait until 'agenthail daemon start'")
			}
		}
		if failed > 0 {
			return fmt.Errorf("channel #%s had %d failed delivery(s)", channelName, failed)
		}
		return nil
	default:
		return fmt.Errorf("unknown channel subcommand '%s'", args[0])
	}
}

func (a *App) cmdRelay(args []string) error {
	if a.Registry == nil {
		return fmt.Errorf("registry not available")
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthail relay <add|list|rm> ...")
	}
	switch args[0] {
	case "add":
		if len(args) < 3 || len(args) > 4 {
			return fmt.Errorf("usage: agenthail relay add <from-target> <to-target> [regex]")
		}
		ctx := context.Background()
		fromSess, _, err := a.resolveTarget(ctx, args[1])
		if err != nil {
			return fmt.Errorf("from-target: %w", err)
		}
		toSess, _, err := a.resolveTarget(ctx, args[2])
		if err != nil {
			return fmt.Errorf("to-target: %w", err)
		}
		if surface.IsReadOnlySession(toSess) {
			return fmt.Errorf("to-target: %s", surface.ReadOnlySessionReason(toSess))
		}
		pattern := ".*"
		if len(args) > 3 {
			pattern = args[3]
		}
		id, err := a.Registry.AddRoute(fromSess.ID, toSess.ID, pattern)
		if err != nil {
			return err
		}
		if _, ok := daemon.IsRunning(); !ok {
			fmt.Fprintf(os.Stderr, "warning: daemon is not running; relay will not fire until you start it (agenthail daemon start)\n")
		}
		fmt.Printf("relay #%d: %s -> %s (pattern /%s/)\n",
			id, a.resolveDisplay(fromSess.ID), a.resolveDisplay(toSess.ID), pattern)
		return nil
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: agenthail relay list")
		}
		routes, err := a.Registry.ListRoutes()
		if err != nil {
			return err
		}
		if len(routes) == 0 {
			fmt.Println("(no relays; use: agenthail relay add <from> <to> [regex])")
			return nil
		}
		for _, r := range routes {
			fmt.Printf("#%-3d %s -> %s /%s/\n",
				r.ID, a.resolveDisplay(r.FromSession), a.resolveDisplay(r.ToSession), r.Pattern)
		}
		return nil
	case "rm", "remove", "delete":
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthail relay rm <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid relay id %q", args[1])
		}
		if err := a.Registry.RemoveRoute(id); err != nil {
			return err
		}
		fmt.Printf("removed relay #%d\n", id)
		return nil
	default:
		return fmt.Errorf("unknown relay subcommand '%s'", args[0])
	}
}

func (a *App) cmdDaemon(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: agenthail daemon <start|stop|restart|status|install|uninstall>")
	}
	switch args[0] {
	case "start":
		return a.daemonStart()
	case "stop":
		return a.daemonStop()
	case "restart":
		return a.daemonRestart()
	case "status":
		return a.daemonStatus()
	case "install":
		return a.daemonInstallService()
	case "uninstall":
		return a.daemonUninstallService()
	default:
		return fmt.Errorf("unknown daemon subcommand '%s'", args[0])
	}
}
