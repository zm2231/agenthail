// Package listeners finds this host's listening TCP sockets and the processes
// that own them without connecting to any of them.
package listeners

import (
	"cmp"
	"slices"
)

type Listener struct {
	Address string
	Port    int
	PID     int
	Command string
	Env     map[string]string
}

// Scan returns each listening socket with its owner's values for the named
// environment variables. A socket whose owner this user cannot inspect has PID 0.
func Scan(variables []string) ([]Listener, error) {
	found, err := scan(variables)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(found, func(a, b Listener) int {
		return cmp.Or(cmp.Compare(a.Port, b.Port), cmp.Compare(a.Address, b.Address), cmp.Compare(a.PID, b.PID))
	})
	return slices.CompactFunc(found, func(a, b Listener) bool {
		return a.Port == b.Port && a.Address == b.Address && a.PID == b.PID
	}), nil
}

func pick(env map[string]string, variables []string) map[string]string {
	out := map[string]string{}
	for _, name := range variables {
		if value := env[name]; value != "" {
			out[name] = value
		}
	}
	return out
}
