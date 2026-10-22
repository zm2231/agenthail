package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/uiconf"
)

const uiUsage = `usage:
  agenthail ui get [--resolved] [--device mac|phone|tablet|web] [--name <device>] [--json]
  agenthail ui set 'key = value' ... [--message <text>]
  agenthail ui unset <key>[@device] ... [--message <text>]
  agenthail ui write <file|-> [--message <text>]
  agenthail ui check <file|->
  agenthail ui log [--limit <n>] [--json]
  agenthail ui diff [<rev>]
  agenthail ui undo [<rev>]
  agenthail ui show-defaults [--docs]
  agenthail ui path`

type uiArgs struct {
	positional []string
	device     string
	name       string
	message    string
	limit      int
	resolved   bool
	json       bool
	docs       bool
}

func parseUIArgs(args []string) (uiArgs, error) {
	parsed := uiArgs{device: "mac", limit: 20}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", arg)
			}
			i++
			return args[i], nil
		}
		var err error
		switch arg {
		case "--resolved":
			parsed.resolved = true
		case "--json":
			parsed.json = true
		case "--docs":
			parsed.docs = true
		case "--device":
			parsed.device, err = value()
		case "--name":
			parsed.name, err = value()
		case "--message":
			parsed.message, err = value()
		case "--limit":
			var text string
			if text, err = value(); err == nil {
				if _, scanErr := fmt.Sscanf(text, "%d", &parsed.limit); scanErr != nil || parsed.limit <= 0 {
					err = errors.New("--limit must be a positive number")
				}
			}
		case "--":
			parsed.positional = append(parsed.positional, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(arg, "--") {
				return parsed, fmt.Errorf("unknown flag %s\n%s", arg, uiUsage)
			}
			parsed.positional = append(parsed.positional, arg)
		}
		if err != nil {
			return parsed, err
		}
	}
	return parsed, nil
}

func (a *App) cmdUI(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(os.Stdout, uiUsage)
		return nil
	}
	command := args[0]
	parsed, err := parseUIArgs(args[1:])
	if err != nil {
		return err
	}
	switch command {
	case "show-defaults":
		fmt.Fprint(os.Stdout, uiconf.ShowDefaults(parsed.docs))
		return nil
	case "check":
		text, err := readUISource(parsed.positional, "check")
		if err != nil {
			return err
		}
		return a.printUIProblems(uiconf.Parse(text).Problems, "ui.conf: no problems")
	}
	store, err := uiconf.Open(a.uiDir())
	if err != nil {
		return err
	}
	switch command {
	case "path":
		fmt.Fprintln(os.Stdout, store.Path())
		return nil
	case "get":
		return a.uiGet(store, parsed)
	case "set":
		return a.uiSet(store, parsed)
	case "unset":
		return a.uiUnset(store, parsed)
	case "write":
		text, err := readUISource(parsed.positional, "write")
		if err != nil {
			return err
		}
		if problems := uiconf.Parse(text).Problems; len(problems) > 0 {
			_ = a.printUIProblems(problems, "")
			return errors.New("ui.conf not written: fix the lines above, or check them with agenthail ui check")
		}
		message := parsed.message
		if message == "" {
			message = "Replace ui.conf"
		}
		state, changed, err := store.Write(text, a.uiAuthor(), message)
		return a.reportUIChange(state, changed, err)
	case "log":
		entries, err := store.Log(parsed.limit)
		if err != nil {
			return err
		}
		if parsed.json {
			return json.NewEncoder(os.Stdout).Encode(entries)
		}
		for _, e := range entries {
			fmt.Fprintf(os.Stdout, "%s  %s  %-12s %s\n", e.Rev, e.Time.Local().Format("2006-01-02 15:04"), e.Author, e.Message)
		}
		return nil
	case "diff":
		if len(parsed.positional) > 1 {
			return errors.New(uiUsage)
		}
		diff, err := store.Diff(first(parsed.positional))
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stdout, diff)
		return nil
	case "undo":
		if len(parsed.positional) > 1 {
			return errors.New(uiUsage)
		}
		state, entry, err := store.Undo(first(parsed.positional), a.uiAuthor())
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "committed %s: %s\n", entry.Rev, entry.Message)
		return a.printUIProblems(state.Parsed.Problems, "")
	}
	return fmt.Errorf("unknown ui command %q\n%s", command, uiUsage)
}

func (a *App) uiGet(store *uiconf.Store, parsed uiArgs) error {
	state, err := store.Current()
	if err != nil {
		return err
	}
	if !parsed.resolved {
		if parsed.json {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"rev": state.Rev, "text": state.Text, "problems": nonNilProblems(state.Parsed.Problems)})
		}
		fmt.Fprint(os.Stdout, state.Text)
		return nil
	}
	values := state.Parsed.Resolve(uiconf.Device{Class: parsed.device, Name: parsed.name})
	if parsed.json {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"rev": state.Rev, "values": values, "problems": nonNilProblems(state.Parsed.Problems)})
	}
	for _, key := range uiconf.Keys {
		value := values[key.Name]
		if list, ok := value.([]string); ok {
			value = strings.Join(list, ", ")
			if value == "" {
				value = "none"
			}
		}
		fmt.Fprintf(os.Stdout, "%s = %v\n", key.Name, value)
	}
	return a.printUIProblems(state.Parsed.Problems, "")
}

func (a *App) uiSet(store *uiconf.Store, parsed uiArgs) error {
	if len(parsed.positional) == 0 {
		return errors.New(uiUsage)
	}
	var assignments []uiconf.Assignment
	for _, line := range parsed.positional {
		assignment, err := uiconf.ParseAssignment(line)
		if err != nil {
			return err
		}
		if path, ok := strings.CutPrefix(assignment.Value, "image: "); ok && !strings.HasPrefix(path, "backgrounds/") {
			rel, err := store.ImportBackground(expandHome(path))
			if err != nil {
				return err
			}
			assignment.Value = "image: " + rel
		}
		assignments = append(assignments, assignment)
	}
	message := parsed.message
	if message == "" {
		var names []string
		for _, assignment := range assignments {
			names = append(names, strings.TrimSpace(strings.SplitN(assignmentLine(assignment), "=", 2)[0]))
		}
		message = "Set " + strings.Join(names, ", ")
	}
	state, changed, err := store.Update(a.uiAuthor(), message, func(text string) string { return uiconf.Set(text, assignments...) })
	return a.reportUIChange(state, changed, err)
}

func (a *App) uiUnset(store *uiconf.Store, parsed uiArgs) error {
	if len(parsed.positional) == 0 {
		return errors.New(uiUsage)
	}
	for _, name := range parsed.positional {
		key, _, _ := strings.Cut(name, "@")
		if _, ok := uiconf.Lookup(key); !ok {
			return fmt.Errorf("unknown key %s", key)
		}
	}
	message := parsed.message
	if message == "" {
		message = "Reset " + strings.Join(parsed.positional, ", ")
	}
	state, changed, err := store.Update(a.uiAuthor(), message, func(text string) string {
		for _, name := range parsed.positional {
			key, device, _ := strings.Cut(name, "@")
			text = uiconf.Unset(text, key, device)
		}
		return text
	})
	return a.reportUIChange(state, changed, err)
}

func assignmentLine(a uiconf.Assignment) string {
	if a.Device == "" {
		return a.Key + " = " + a.Value
	}
	return a.Key + "@" + a.Device + " = " + a.Value
}

func (a *App) reportUIChange(state uiconf.State, changed bool, err error) error {
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintf(os.Stdout, "unchanged at %s\n", state.Rev)
		return nil
	}
	fmt.Fprintf(os.Stdout, "committed %s by %s\n", state.Rev, a.uiAuthor())
	return a.printUIProblems(state.Parsed.Problems, "")
}

func (a *App) printUIProblems(problems []uiconf.Problem, clean string) error {
	if len(problems) == 0 {
		if clean != "" {
			fmt.Fprintln(os.Stdout, clean)
		}
		return nil
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stdout, p.String())
	}
	return nil
}

func (a *App) uiDir() string {
	if a.UIDir != "" {
		return a.UIDir
	}
	return uiconf.DefaultDir()
}

// uiAuthor names who a change is from: the calling agent's alias when an
// agent runs the command, otherwise the person.
func (a *App) uiAuthor() string {
	if a.uiAuthorName != "" {
		return a.uiAuthorName
	}
	a.uiAuthorName = uiconf.Person
	if card, err := a.callerCard(); err == nil && card.Resolved {
		switch {
		case card.Alias != "":
			a.uiAuthorName = "@" + card.Alias
		case card.Session != "":
			a.uiAuthorName = "@" + shortID(card.Session)
		}
	}
	return a.uiAuthorName
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return path
}

func readUISource(positional []string, verb string) (string, error) {
	if len(positional) != 1 {
		return "", fmt.Errorf("usage: agenthail ui %s <file|->", verb)
	}
	if positional[0] == "-" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		return string(data), err
	}
	data, err := os.ReadFile(expandHome(positional[0]))
	return string(data), err
}

func nonNilProblems(problems []uiconf.Problem) []uiconf.Problem {
	if problems == nil {
		return []uiconf.Problem{}
	}
	return problems
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
