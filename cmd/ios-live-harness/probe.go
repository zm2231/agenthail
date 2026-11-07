package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var probeCommands = [][]string{{"git", "hash-object", "--stdin"}, {"sleep", "30"}}

const probeWait = 10 * time.Second

type probeSet struct {
	mu   sync.Mutex
	pids map[int]scope
}

func (p *probeSet) roots() map[int]scope {
	p.mu.Lock()
	defer p.mu.Unlock()
	roots := make(map[int]scope, len(p.pids))
	for pid, s := range p.pids {
		roots[pid] = s
	}
	return roots
}

func (p *probeSet) set(pid int, s *scope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pids == nil {
		p.pids = map[int]scope{}
	}
	if s == nil {
		delete(p.pids, pid)
	} else {
		p.pids[pid] = *s
	}
}

func (h *harness) probeTools() error {
	for _, host := range h.hosts {
		for _, command := range probeCommands {
			if err := h.probe(host, command); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *harness) probe(host *host, command []string) error {
	cmd := exec.Command(filepath.Join(host.dir("bin"), command[0]), command[1:]...)
	cmd.Env, cmd.Dir = host.env(), host.root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s %s: %w", host.name, command[0], err)
	}
	pid := cmd.Process.Pid
	s := h.scope(host)
	h.probes.set(pid, &s)
	seen := h.watch.waitForProgram(pid, probeWait)
	stdin.Close()
	cmd.Process.Kill()
	cmd.Wait()
	h.probes.set(pid, nil)
	if !seen {
		h.watch.missed(fmt.Sprintf("the watcher never saw %s run %s from its PATH", host.name, command[0]))
	}
	return nil
}
