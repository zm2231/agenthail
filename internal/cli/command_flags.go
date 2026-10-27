package cli

import (
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

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
	valueFlags := map[string]bool{"--request": true, "--reason": true, "--answers": true, "--when": true, "--effort": true, "--mode": true, "--service-tier": true, "--output-schema": true, "--cwd": true, "--target": true, "--alias": true, "--before": true, "--last-turn": true, "--id": true, "--ids": true, "--client-id": true, "--cursor": true, "--from": true, "--model": true, "--timeout": true, "--max-bytes": true, "--codex-recent-hours": true, "--tailscale": true, "--address": true, "--name": true, "--host": true, "--label": true, "--project": true, "--instructions": true, "--permission-mode": true, "--wait": true, "--root": true, "--enabled": true, "--idle": true, "--to": true, "--remote-control": true, "--key-file": true, "--key-env": true, "--keychain-service": true, "--keychain-account": true}
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
		"list": {values: map[string]bool{"--cwd": true, "--project": true}, bools: map[string]bool{"--all": true, "--archived": true, "--recent": true, "--running": true, "--wide": true, "--json": true}}, "ls": {values: map[string]bool{"--cwd": true, "--project": true}, bools: map[string]bool{"--all": true, "--archived": true, "--recent": true, "--running": true, "--wide": true, "--json": true}}, "runs": {bools: map[string]bool{"--json": true}}, "whoami": {bools: map[string]bool{"--json": true}}, "roots": {bools: map[string]bool{"--json": true}}, "usage": {bools: map[string]bool{"--json": true}}, "proxies": {values: map[string]bool{"--key-file": true, "--key-env": true, "--keychain-service": true, "--keychain-account": true}, bools: map[string]bool{"--json": true, "--key-command": true}}, "claude": {values: map[string]bool{"--root": true, "--model": true, "--enabled": true, "--idle": true}, bools: map[string]bool{"--json": true}}, "search": {bools: map[string]bool{"--json": true}},
		"send":  {values: surface.SendValueFlags, bools: surface.SendBoolFlags},
		"reply": {values: map[string]bool{"--before": true, "--timeout": true, "--max-bytes": true}, bools: map[string]bool{"--json": true, "--report": true}}, "last": {values: map[string]bool{"--before": true, "--timeout": true, "--max-bytes": true}, bools: map[string]bool{"--full": true, "--json": true, "--report": true}}, "tail": {values: map[string]bool{"--before": true, "--timeout": true, "--max-bytes": true}, bools: map[string]bool{"--full": true, "--json": true, "--report": true}},
		"goal": {bools: map[string]bool{"--json": true}}, "queue": {}, "history": {bools: map[string]bool{"--json": true}},
		"thread":  {values: map[string]bool{"--message": true, "--from": true, "--cwd": true, "--alias": true, "--model": true, "--approval": true, "--timeout": true, "--effort": true, "--mode": true, "--service-tier": true, "--output-schema": true, "--name": true, "--worktree": true, "--agent": true, "--permission-mode": true, "--root": true, "--host": true, "--last-turn": true, "--id": true, "--ids": true, "--client-id": true, "--cursor": true, "--wait": true}, bools: map[string]bool{"--json": true, "--help": true, "--stop-terminal": true}},
		"channel": {},
		"project": {values: map[string]bool{"--host": true, "--name": true}, bools: map[string]bool{"--all": true, "--json": true, "--help": true}}, "projects": {values: map[string]bool{"--host": true, "--name": true}, bools: map[string]bool{"--all": true, "--json": true, "--help": true}},
		"hosts":   {values: map[string]bool{"--address": true, "--name": true}, bools: map[string]bool{"--json": true, "--help": true}},
		"move":    {values: map[string]bool{"--to": true, "--wait": true, "--timeout": true}, bools: map[string]bool{"--json": true, "--help": true, "--stop-terminal": true, "--unfinished": true}},
		"devices": {values: map[string]bool{"--name": true}, bools: map[string]bool{"--json": true, "--help": true}},
		"signin":  {values: map[string]bool{"--label": true}, bools: map[string]bool{"--json": true, "--help": true}},
		"person":  {bools: map[string]bool{"--reset": true, "--json": true}},
		"doctor":  {bools: map[string]bool{"--json": true}}, "permissions": {bools: map[string]bool{"--json": true}}, "version": {bools: map[string]bool{"--json": true}}, "--version": {bools: map[string]bool{"--json": true}},
		"update": {bools: map[string]bool{"--check": true, "--json": true, "--help": true}}, "upgrade": {bools: map[string]bool{"--check": true, "--json": true, "--help": true}},
		"stream": {values: map[string]bool{"--timeout": true}}, "compact": {}, "model": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}},
		"permission-mode": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "requests": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}},
		"answer": {values: map[string]bool{"--timeout": true, "--request": true, "--reason": true, "--answers": true}, bools: map[string]bool{"--json": true}}, "interrupt": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "steer": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "control": {values: map[string]bool{"--timeout": true}, bools: map[string]bool{"--json": true}}, "identify": {}, "coordinator": {values: map[string]bool{"--name": true, "--instructions": true, "--permission-mode": true, "--cwd": true, "--model": true, "--host": true}, bools: map[string]bool{"--json": true, "--replace": true}}, "relay": {}, "daemon": {}, "daemon-run": {}, "hook": {values: map[string]bool{"--claude-pid": true, "--surface": true}}, "hooks": {values: map[string]bool{"--remote-control": true}}, "launch": {}, "dashboard": {values: map[string]bool{"--codex-recent-hours": true, "--tailscale": true}, bools: map[string]bool{"--no-open": true, "--json": true}}, "help": {}, "-h": {}, "--help": {},
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
