// Package portholder names the process holding a local TCP port, so a
// listener that cannot bind can say who has its port.
package portholder

import (
	"fmt"
	"strings"
)

const commandClip = 200

type Holder struct {
	PID     int
	Command string
}

func (h Holder) String() string {
	if h.PID == 0 {
		return "a process this user cannot see"
	}
	return fmt.Sprintf("pid %d (%s)", h.PID, h.Command)
}

func Describe(port int) string {
	holders, err := Find(port)
	if err != nil || len(holders) == 0 {
		return ""
	}
	names := make([]string, len(holders))
	for i, h := range holders {
		names[i] = h.String()
	}
	return strings.Join(names, ", ")
}

func clip(command string) string {
	command = strings.TrimSpace(command)
	if len(command) > commandClip {
		return command[:commandClip] + "..."
	}
	return command
}
