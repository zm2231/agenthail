package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

var systemDirs = []string{"/System/", "/usr/lib/", "/usr/share/", "/Library/Apple/", "/private/var/db/"}

type inventory struct {
	out       io.Writer
	samples   int
	seen      map[string]bool
	ports     map[string]int
	listening map[string]bool
}

func newInventory(out io.Writer) *inventory {
	return &inventory{out: out, seen: map[string]bool{}, ports: map[string]int{}, listening: map[string]bool{}}
}

func (inv *inventory) sample(scopes []scope) {
	inv.samples++
	for _, s := range scopes {
		inv.ports[s.name] = s.port
	}
}

func (inv *inventory) note(s scope, e endpoint) {
	if e.kind == "tcp" && e.listening && e.local.Port() == uint16(s.port) {
		inv.listening[s.name] = true
	}
	key := s.name + ": " + describe(s, e)
	if inv.seen[key] {
		return
	}
	inv.seen[key] = true
	fmt.Fprintf(inv.out, "ios-live-harness: saw %s\n", key)
}

func (inv *inventory) problems() []string {
	if inv.samples == 0 {
		return []string{"the watcher took no samples of the host processes"}
	}
	var out []string
	for name, port := range inv.ports {
		if !inv.listening[name] {
			out = append(out, fmt.Sprintf("the watcher never saw %s's own listener on port %d", name, port))
		}
	}
	sort.Strings(out)
	return out
}

func (inv *inventory) summary() string {
	return fmt.Sprintf("the watcher took %d samples and saw %d distinct descriptors", inv.samples, len(inv.seen))
}

func describe(s scope, e endpoint) string {
	switch e.kind {
	case "tcp", "udp":
		switch {
		case e.listening:
			return fmt.Sprintf("%s listening on %s", e.kind, e.local)
		case e.local.Port() == uint16(s.port):
			return fmt.Sprintf("%s connection to its own port %s from %s", e.kind, e.local, e.remote.Addr())
		case e.remote.Port() == uint16(s.port) || e.remote.Port() == uint16(s.trap):
			return fmt.Sprintf("%s connection from %s to %s", e.kind, e.local.Addr(), e.remote)
		}
		return fmt.Sprintf("%s %s -> %s", e.kind, e.local, e.remote)
	case "unix":
		return fmt.Sprintf("unix %q peer %q", e.path, e.peer)
	case "file":
		for _, dir := range systemDirs {
			if strings.HasPrefix(e.path, dir) {
				return "files under " + dir
			}
		}
	}
	return fmt.Sprintf("%s %q", e.kind, e.path)
}
