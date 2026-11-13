package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// A copy of the harness in each host's bin answers as Claude, so a claude-bg start reaches the daemon's real start path with no Claude CLI on the machine.
const stubClaudeName = "claude"

type stubAgent struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
}

func (h *host) installStubClaude() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	source, err := os.Open(self)
	if err != nil {
		return "", err
	}
	defer source.Close()
	path := filepath.Join(h.dir("bin"), stubClaudeName)
	target, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return "", err
	}
	if err := target.Close(); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func runStubClaude(args []string, stdout, stderr io.Writer) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	agents := filepath.Join(home, ".claude", "live-agents.json")
	switch {
	case len(args) >= 1 && args[0] == "--version":
		fmt.Fprintln(stdout, "0.0.0 (live harness stub)")
		return 0
	case len(args) >= 3 && args[0] == "agents" && args[1] == "--json" && args[2] == "--all":
		list, err := readStubAgents(agents)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		json.NewEncoder(stdout).Encode(list)
		return 0
	case len(args) >= 1 && args[0] == "--bg":
		agent, err := startStubAgent(home, agents, args[1:])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "backgrounded · %s\n", agent.ID)
		return 0
	}
	fmt.Fprintf(stderr, "the live harness stub runs only --bg, agents --json --all and --version, not %q\n", args)
	return 2
}

func startStubAgent(home, agents string, args []string) (stubAgent, error) {
	var name, message string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				name = args[i+1]
			}
			i++
		case "--model", "--agent", "--permission-mode", "--effort":
			i++
		case "--":
			if i+1 < len(args) {
				message = args[i+1]
			}
			i = len(args)
		}
	}
	if message == "" {
		return stubAgent{}, errors.New("a background start needs a message after --")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return stubAgent{}, err
	}
	short := make([]byte, 4)
	if _, err := rand.Read(short); err != nil {
		return stubAgent{}, err
	}
	agent := stubAgent{ID: hex.EncodeToString(short), SessionID: uuid.NewString(), Cwd: cwd, Name: name, Kind: "background", State: "idle"}
	dir := filepath.Join(home, ".claude", "projects", claudeProjectDirUnsafe.ReplaceAllString(cwd, "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return stubAgent{}, err
	}
	if err := appendRecords(filepath.Join(dir, agent.SessionID+".jsonl"), map[string]any{"type": "user", "uuid": uuid.NewString(), "sessionId": agent.SessionID, "cwd": cwd,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": message}}); err != nil {
		return stubAgent{}, err
	}
	list, err := readStubAgents(agents)
	if err != nil {
		return stubAgent{}, err
	}
	data, err := json.Marshal(append(list, agent))
	if err != nil {
		return stubAgent{}, err
	}
	staged := agents + ".tmp"
	if err := os.WriteFile(staged, data, 0o600); err != nil {
		return stubAgent{}, err
	}
	return agent, os.Rename(staged, agents)
}

func readStubAgents(path string) ([]stubAgent, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []stubAgent{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []stubAgent
	return list, json.Unmarshal(data, &list)
}
