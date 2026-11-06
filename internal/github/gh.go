package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type UnavailableError struct {
	Reason string
}

func (e *UnavailableError) Error() string { return e.Reason }

var ErrNotFound = errors.New("pull request not found")

type GH struct {
	Path string
}

type graphqlError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

type graphqlReply struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []graphqlError             `json:"errors"`
}

func (g GH) graphql(ctx context.Context, host, query string) (graphqlReply, error) {
	path := g.Path
	if path == "" {
		found, err := exec.LookPath("gh")
		if err != nil {
			return graphqlReply{}, &UnavailableError{Reason: "the GitHub CLI (gh) is not installed on this host"}
		}
		path = found
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "api", "graphql", "--hostname", host, "-f", "query="+query)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && exit.ExitCode() == 4 {
		return graphqlReply{}, &UnavailableError{Reason: fmt.Sprintf("gh is not signed in to %s; run gh auth login", host)}
	}
	if strings.Contains(stdout.String()+stderr.String(), "Bad credentials") {
		return graphqlReply{}, &UnavailableError{Reason: fmt.Sprintf("GitHub refused gh's sign-in to %s; run gh auth login", host)}
	}
	var reply graphqlReply
	decodeErr := json.Unmarshal(stdout.Bytes(), &reply)
	if runErr != nil && (decodeErr != nil || reply.Data == nil) {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = runErr.Error()
		}
		return graphqlReply{}, fmt.Errorf("gh api graphql: %s", message)
	}
	if decodeErr != nil {
		return graphqlReply{}, fmt.Errorf("gh api graphql: %w", decodeErr)
	}
	return reply, nil
}

func quote(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}
