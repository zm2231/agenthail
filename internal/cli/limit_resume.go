package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
)

const limitResumeUsage = `usage: agenthail limit-resume [list] [--json] | resume <target> | cancel <target> | on | off`

func (a *App) cmdLimitResume(args []string) error {
	positional := stripFlags(args)
	if len(positional) == 0 || positional[0] == "list" && len(positional) == 1 {
		return a.listLimitResumes(hasFlag(args, "--json"))
	}
	switch {
	case (positional[0] == "on" || positional[0] == "off") && len(positional) == 1:
		config, err := daemon.LoadDashboardConfig()
		if err != nil {
			return err
		}
		enabled := positional[0] == "on"
		config.ResumeAfterLimit = &enabled
		if err := daemon.SaveDashboardConfig(config); err != nil {
			return err
		}
		if enabled {
			fmt.Println("sessions a usage limit stopped resume once it resets")
		} else {
			fmt.Println("sessions a usage limit stopped are not resumed; their queued messages still wait for the reset")
		}
		return nil
	case (positional[0] == "resume" || positional[0] == "cancel") && len(positional) == 2:
		ctx, cancel, err := commandContext(args)
		if err != nil {
			return err
		}
		defer cancel()
		sess, _, remote, err := a.resolveAnyTarget(ctx, positional[1])
		if err != nil {
			return err
		}
		action, label := "limit-resume", positional[1]
		if positional[0] == "cancel" {
			action = "limit-resume-cancel"
		}
		var id string
		if remote != nil {
			id, label = remote.ID, remote.label()
		} else {
			id, label = sess.ID, a.resolveDisplay(sess.ID)
		}
		client, err := a.daemonAPI()
		if err != nil {
			return err
		}
		if _, _, err := client.action(ctx, map[string]any{"action": action, "sessionId": id}); err != nil {
			return fmt.Errorf("%s %s: %w", positional[0], label, err)
		}
		if positional[0] == "resume" {
			fmt.Printf("resumed %s\n", label)
		} else {
			fmt.Printf("%s will not be resumed after its usage limit\n", label)
		}
		return nil
	}
	return errors.New(limitResumeUsage)
}

func (a *App) listLimitResumes(asJSON bool) error {
	r := a.registry()
	if r == nil {
		return fmt.Errorf("the registry is unavailable")
	}
	resumes, err := r.LimitResumes()
	if err != nil {
		return err
	}
	if asJSON {
		type row struct {
			SessionID string                    `json:"sessionId"`
			Name      string                    `json:"name"`
			Account   string                    `json:"account,omitempty"`
			State     registry.LimitResumeState `json:"state"`
			RefusedAt time.Time                 `json:"refusedAt"`
			ResetsAt  time.Time                 `json:"resetsAt,omitzero"`
		}
		rows := []row{}
		for _, resume := range resumes {
			rows = append(rows, row{SessionID: resume.SessionID, Name: a.resolveDisplay(resume.SessionID), Account: resume.AccountKey, State: resume.State, RefusedAt: resume.RefusedAt, ResetsAt: resume.ResetsAt})
		}
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	if len(resumes) == 0 {
		fmt.Println("no session is stopped by a usage limit")
		return nil
	}
	for _, resume := range resumes {
		resets := "reset time unknown"
		if !resume.ResetsAt.IsZero() {
			resets = "resets " + resume.ResetsAt.Local().Format("Mon 15:04")
		}
		fmt.Printf("%s  %s  %s  (refused %s)\n", a.resolveDisplay(resume.SessionID), resume.State, resets, resume.RefusedAt.Local().Format("Mon 15:04"))
	}
	return nil
}
