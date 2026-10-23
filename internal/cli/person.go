package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func (a *App) cmdPerson(args []string) error {
	positional := stripFlags(args)
	reset := hasFlag(args, "--reset")
	if len(positional) == 0 || positional[0] != "name" || reset && len(positional) > 1 {
		return fmt.Errorf(`usage: agenthail person name [<name>|--reset] [--json]`)
	}
	r := a.registry()
	if r == nil {
		return fmt.Errorf("the registry is unavailable")
	}
	switch {
	case reset:
		if err := r.SetPersonName(""); err != nil {
			return err
		}
	case len(positional) > 1:
		name := strings.TrimSpace(strings.Join(positional[1:], " "))
		if name == "" {
			return fmt.Errorf("the name is empty; use --reset for the account's name")
		}
		if err := r.SetPersonName(name); err != nil {
			return err
		}
	}
	name, err := r.PersonName()
	if err != nil {
		return err
	}
	setting, err := r.PersonNameSetting()
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"name": name, "set": setting != ""})
	}
	fmt.Println(describePersonName(name, setting != ""))
	return nil
}

func describePersonName(name string, set bool) string {
	if set {
		return fmt.Sprintf("person: %s", name)
	}
	return fmt.Sprintf("person: %s (this account's name; set yours with: agenthail person name \"<name>\")", name)
}
