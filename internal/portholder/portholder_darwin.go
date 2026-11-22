package portholder

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const lsofTimeout = 3 * time.Second

func Find(port int) ([]Holder, error) {
	path, err := exec.LookPath("lsof")
	if err != nil {
		path = "/usr/sbin/lsof"
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsofTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-n", "-P", "-w", "-iTCP:"+strconv.Itoa(port), "-F", "pcnT").Output()
	if exit := (*exec.ExitError)(nil); err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return nil, err
	}
	return parseLsof(string(out), port), nil
}

// lsof -F pcnT: p starts a process, c is its command, n a socket's addresses
// as local or local->remote, then T lines with its TCP state; -iTCP:port
// matches either end, so only a local end on port counts.
func parseLsof(out string, port int) []Holder {
	suffix := ":" + strconv.Itoa(port)
	var listening, other []Holder
	pid, command := 0, ""
	seen := map[int]bool{}
	local := false
	add := func(listen bool) {
		if pid == 0 || seen[pid] {
			return
		}
		seen[pid] = true
		h := Holder{PID: pid, Command: clip(command)}
		if listen {
			listening = append(listening, h)
		} else {
			other = append(other, h)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(value)
			command = ""
		case 'c':
			command = value
		case 'n':
			end, _, _ := strings.Cut(value, "->")
			local = strings.HasSuffix(end, suffix)
		case 'T':
			if local && strings.HasPrefix(value, "ST=") {
				add(value == "ST=LISTEN")
				local = false
			}
		}
	}
	return append(listening, other...)
}
