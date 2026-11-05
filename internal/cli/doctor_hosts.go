package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type hostPairingProblem struct {
	Host    string `json:"host"`
	Problem string `json:"problem"`
}

func (p hostPairingProblem) String() string {
	return fmt.Sprintf("[host] %s: %s", p.Host, p.Problem)
}

func (a *App) hostPairingProblems(ctx context.Context) ([]hostPairingProblem, error) {
	problems := []hostPairingProblem{}
	r := a.registry()
	if r == nil {
		return problems, nil
	}
	if paired, err := r.ListHosts(); err != nil || len(paired) == 0 {
		return problems, err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return nil, err
	}
	data, status, err := client.call(ctx, http.MethodGet, "/api/v1/hosts", nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, actionError(data)
	}
	var listing struct {
		Hosts []struct {
			Name    string `json:"name"`
			Problem string `json:"problem"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(data, &listing); err != nil {
		return nil, err
	}
	for _, host := range listing.Hosts {
		if host.Problem != "" {
			problems = append(problems, hostPairingProblem{Host: host.Name, Problem: host.Problem})
		}
	}
	return problems, nil
}
