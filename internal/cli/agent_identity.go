package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const identityUsage = "usage: agenthail identity <add <name>|rm <name>|list> [--json]"

func (a *App) cmdIdentity(args []string) error {
	positional := stripFlags(args)
	if len(positional) == 0 {
		positional = []string{"list"}
	}
	reg := a.registry()
	jsonOut := hasFlag(args, "--json")
	switch positional[0] {
	case "add":
		if len(positional) != 2 {
			return errors.New("usage: agenthail identity add <name> [--json]")
		}
		identity, err := reg.AddAgentIdentity(strings.TrimPrefix(positional[1], "@"))
		if err != nil {
			return err
		}
		if jsonOut {
			return emit(identity)
		}
		fmt.Printf("added identity @%s\n", identity.Name)
		fmt.Printf("send as it: export AGENTHAIL_SESSION_ID=%s (or pass --from %s), then agenthail send <target> \"message\"\n", identity.Name, identity.Name)
		fmt.Printf("read what is sent to it: agenthail inbox %s [--wait 10m]\n", identity.Name)
		return nil
	case "rm", "remove":
		if len(positional) != 2 {
			return errors.New("usage: agenthail identity rm <name>")
		}
		name := strings.TrimPrefix(positional[1], "@")
		if err := reg.RemoveAgentIdentity(name); err != nil {
			return err
		}
		if jsonOut {
			return emit(map[string]any{"removed": name})
		}
		fmt.Printf("removed identity @%s and its inbox\n", name)
		return nil
	case "list", "ls":
		if len(positional) != 1 {
			return errors.New("usage: agenthail identity list [--json]")
		}
		identities, err := reg.AgentIdentities()
		if err != nil {
			return err
		}
		if jsonOut {
			return emit(map[string]any{"identities": identities})
		}
		if len(identities) == 0 {
			fmt.Println("(no identities; add one with: agenthail identity add <name>)")
			return nil
		}
		for _, identity := range identities {
			fmt.Printf("@%-20s unread=%d total=%d since %s\n", identity.Name, identity.Unread, identity.Total, identity.CreatedAt.Local().Format(time.DateTime))
		}
		return nil
	}
	return errors.New(identityUsage)
}

const inboxUsage = "usage: agenthail inbox [<name>] [--all] [--peek] [--limit <n>] [--wait <duration>] [--json] | agenthail inbox [<name>] --prune <age>"

func (a *App) callerIdentity() (string, error) {
	for _, source := range surface.CallerIdentity {
		value := os.Getenv(source.Variable)
		if value == "" {
			continue
		}
		if source.Surface == "" && a.namesAgentIdentity(value) {
			id, err := a.registry().ResolveTarget(value)
			if err != nil {
				return "", err
			}
			identity, err := a.registry().AgentIdentityOf(id)
			return identity.Name, err
		}
		return "", fmt.Errorf("%s names a session, not an agent identity; name the identity: %s", source.Variable, inboxUsage)
	}
	return "", errors.New("name the identity whose inbox to read, or set AGENTHAIL_SESSION_ID to it; " + inboxUsage)
}

func (a *App) cmdInbox(args []string) error {
	positional := stripFlags(args)
	if len(positional) > 1 {
		return errors.New(inboxUsage)
	}
	reg := a.registry()
	name := ""
	if len(positional) == 1 {
		name = strings.TrimPrefix(positional[0], "@")
	} else {
		var err error
		if name, err = a.callerIdentity(); err != nil {
			return err
		}
	}
	if _, err := reg.AgentIdentityNamed(name); err != nil {
		return err
	}
	jsonOut := hasFlag(args, "--json")
	if prune := flagVal(args, "--prune"); prune != "" {
		age, err := parseAge(prune)
		if err != nil {
			return err
		}
		removed, err := reg.PruneInbox(name, time.Now().Add(-age))
		if err != nil {
			return err
		}
		if jsonOut {
			return emit(map[string]any{"identity": name, "pruned": removed})
		}
		fmt.Printf("pruned %d read message(s) older than %s from @%s\n", removed, prune, name)
		return nil
	}
	query := registry.InboxQuery{All: hasFlag(args, "--all"), Peek: hasFlag(args, "--peek")}
	if limit := flagVal(args, "--limit"); limit != "" {
		n, err := strconv.Atoi(limit)
		if err != nil || n <= 0 {
			return errors.New("--limit must be a positive number")
		}
		query.Limit = n
	}
	var wait time.Duration
	if value := flagVal(args, "--wait"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return errors.New("--wait must be a positive duration such as 30s or 10m")
		}
		wait = parsed
	}
	if wait > 0 {
		if err := waitForInbox(reg, name, wait); err != nil {
			return err
		}
	}
	messages, err := reg.ReadInbox(name, query)
	if err != nil {
		return err
	}
	if jsonOut {
		return emit(map[string]any{"identity": name, "messages": messages})
	}
	if len(messages) == 0 {
		fmt.Printf("no unread messages for @%s\n", name)
		return nil
	}
	for index, message := range messages {
		if index > 0 {
			fmt.Println()
		}
		sender := message.SenderName
		if sender == "" {
			sender = message.SenderID
		}
		fmt.Printf("#%d %s from %s\n%s\n", message.ID, message.ReceivedAt.Local().Format(time.DateTime), sender, message.Message)
	}
	return nil
}

func waitForInbox(reg *registry.Registry, name string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		unread, err := reg.UnreadInbox(name)
		if err != nil || unread > 0 {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("no message for @%s within %s", name, wait)
		}
		time.Sleep(min(time.Second, time.Until(deadline)))
	}
}

func parseAge(value string) (time.Duration, error) {
	if days, found := strings.CutSuffix(value, "d"); found {
		n, err := strconv.Atoi(days)
		if err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	} else if age, err := time.ParseDuration(value); err == nil && age > 0 {
		return age, nil
	}
	return 0, fmt.Errorf("--prune takes an age such as 30d or 12h, not %q", value)
}
