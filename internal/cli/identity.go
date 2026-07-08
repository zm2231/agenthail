package cli

import (
	"context"
	"fmt"
	"strings"
)

func (a *App) cmdIdentify(args []string) error {
	if a.Registry == nil {
		return fmt.Errorf("registry not available")
	}
	if len(args) == 0 || args[0] == "list" {
		return a.identifyList()
	}
	if len(args) < 2 {
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
		fmt.Printf("@%-20s %s\n", r.Name, truncate(r.SessionID, 40))
	}
	return nil
}

func (a *App) cmdChannel(args []string) error {
	if a.Registry == nil {
		return fmt.Errorf("registry not available")
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthail channel <create|add|list|send> ...")
	}
	ctx := context.Background()
	switch args[0] {
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: agenthail channel create <name>")
		}
		id, err := a.Registry.CreateChannel(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("created channel #%s (id %s)\n", args[1], id)
		return nil
	case "add":
		if len(args) < 3 {
			return fmt.Errorf("usage: agenthail channel add <channel> <target>")
		}
		channelName := strings.TrimPrefix(args[1], "#")
		sess, _, err := a.resolveTarget(ctx, args[2])
		if err != nil {
			return err
		}
		if err := a.Registry.AddToChannel(channelName, sess.ID); err != nil {
			return err
		}
		fmt.Printf("added %s to #%s\n", truncate(sess.ID, 24), channelName)
		return nil
	case "list":
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
				fmt.Printf("    %s\n", truncate(m, 40))
			}
		}
		return nil
	case "send", "broadcast":
		if len(args) < 3 {
			return fmt.Errorf("usage: agenthail channel send <channel> \"message\"")
		}
		channelName := strings.TrimPrefix(args[1], "#")
		message := strings.Join(args[2:], " ")
		members, err := a.Registry.ChannelMembers(channelName)
		if err != nil {
			return err
		}
		if len(members) == 0 {
			return fmt.Errorf("channel #%s has no members", channelName)
		}
		var sent, failed int
		for _, mid := range members {
			sess, surf, err := a.resolveTarget(ctx, mid)
			if err != nil {
				failed++
				fmt.Printf("  [FAIL] %s: %s\n", truncate(mid, 24), err)
				continue
			}
			if _, err := surf.Send(ctx, sess, message); err != nil {
				failed++
				fmt.Printf("  [FAIL] %s: %s\n", truncate(mid, 24), err)
				continue
			}
			sent++
			fmt.Printf("  [ OK ] %s\n", truncate(mid, 24))
		}
		fmt.Printf("channel #%s: %d sent, %d failed\n", channelName, sent, failed)
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
		if len(args) < 3 {
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
		pattern := ".*"
		if len(args) > 3 {
			pattern = args[3]
		}
		id, err := a.Registry.AddRoute(fromSess.ID, toSess.ID, pattern)
		if err != nil {
			return err
		}
		fmt.Printf("relay #%d: %s -> %s (pattern /%s/)\n", id, truncate(fromSess.ID, 24), truncate(toSess.ID, 24), pattern)
		return nil
	case "list":
		routes, err := a.Registry.ListRoutes()
		if err != nil {
			return err
		}
		if len(routes) == 0 {
			fmt.Println("(no relays; use: agenthail relay add <from> <to> [regex])")
			return nil
		}
		for _, r := range routes {
			fmt.Printf("#%-3d %-24s -> %-24s /%s/\n", r.ID, truncate(r.FromSession, 24), truncate(r.ToSession, 24), r.Pattern)
		}
		return nil
	case "rm", "remove", "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: agenthail relay rm <id>")
		}
		var id int64
		fmt.Sscanf(args[1], "%d", &id)
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
	if len(args) == 0 {
		return fmt.Errorf("usage: agenthail daemon <start|stop|status>")
	}
	switch args[0] {
	case "start":
		return a.daemonStart()
	case "stop":
		return a.daemonStop()
	case "status":
		return a.daemonStatus()
	default:
		return fmt.Errorf("unknown daemon subcommand '%s'", args[0])
	}
}
