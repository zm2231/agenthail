package listeners

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func scan(variables []string) ([]Listener, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "+c", "0", "-F", "pctn").Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0) {
		return nil, err
	}
	return parseLsof(out, variables), nil
}

func parseLsof(out []byte, variables []string) []Listener {
	var found []Listener
	var pid int
	var command, family string
	var env map[string]string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(value)
			command, env = "", nil
		case 'c':
			command = value
		case 't':
			family = value
		case 'n':
			cut := strings.LastIndexByte(value, ':')
			port, err := strconv.Atoi(value[cut+1:])
			if cut < 0 || err != nil {
				continue
			}
			address := strings.Trim(value[:cut], "[]")
			if address == "*" {
				address = map[string]string{"IPv6": "::"}[family]
				if address == "" {
					address = "0.0.0.0"
				}
			}
			if env == nil {
				var path string
				path, env = procArgs(pid, variables)
				if path != "" {
					command = filepath.Base(path)
				}
			}
			found = append(found, Listener{Address: address, Port: port, PID: pid, Command: command, Env: env})
		}
	}
	return found
}

func procArgs(pid int, variables []string) (string, map[string]string) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return "", map[string]string{}
	}
	argc := int(binary.NativeEndian.Uint32(raw))
	fields := bytes.Split(raw[4:], []byte{0})
	path := string(fields[0])
	env := map[string]string{}
	index := 1
	for index < len(fields) && len(fields[index]) == 0 {
		index++
	}
	index += argc
	for ; index < len(fields) && len(fields[index]) > 0; index++ {
		name, value, _ := strings.Cut(string(fields[index]), "=")
		env[name] = value
	}
	return path, pick(env, variables)
}
