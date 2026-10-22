package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const moveUsage = `usage: agenthail move <target> --to <host> [--wait <duration>] [--stop-terminal] [--json]
       agenthail move list [--unfinished] [--json]`

func (a *App) cmdMove(args []string) (moveErr error) {
	if hasFlag(args, "--help") {
		fmt.Println(moveUsage)
		return nil
	}
	jsonOut := hasFlag(args, "--json")
	positional := stripFlags(args)
	if len(positional) >= 1 && positional[0] == "list" {
		return a.cmdMoveList(args)
	}
	answered := false
	defer func() {
		if moveErr != nil && jsonOut && !answered {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "error": moveErr.Error()})
		}
	}()
	host := flagVal(args, "--to")
	if len(positional) != 1 || host == "" {
		return errors.New(moveUsage)
	}
	wait := time.Duration(0)
	if value := flagVal(args, "--wait"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < 0 {
			return fmt.Errorf("--wait must be a duration such as 5m")
		}
		wait = parsed
	}
	timeout, err := commandTimeout(args, 30*time.Minute+wait)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	session, _, remote, err := a.resolveAnyTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		return fmt.Errorf("%s runs on another host; run agenthail move there", positional[0])
	}
	api, err := a.daemonAPI()
	if err != nil {
		return err
	}
	data, status, err := api.call(ctx, http.MethodPost, "/api/v1/session-moves", nil, map[string]any{"sessionId": session.ID, "host": host, "stopTerminal": hasFlag(args, "--stop-terminal"), "waitSeconds": int(wait / time.Second)}, nil)
	if err != nil && len(data) == 0 {
		var answer struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(err.Error()), &answer) == nil && answer.Error != "" {
			return errors.New(answer.Error)
		}
		return err
	}
	if jsonOut {
		answered = true
		_, writeErr := os.Stdout.Write(append(data, '\n'))
		if status != http.StatusOK {
			return errMoveNotDone
		}
		return writeErr
	}
	var answer struct {
		OK      bool   `json:"ok"`
		Unknown bool   `json:"unknown"`
		Error   string `json:"error"`
		Result  struct {
			Move struct {
				ID string `json:"id"`
			} `json:"move"`
			Host struct {
				Name string `json:"name"`
			} `json:"host"`
			Stopped string `json:"stopped"`
			Alias   string `json:"alias"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return err
	}
	label := session.ID
	if session.Name != "" {
		label = session.Name + " (" + session.ID + ")"
	}
	switch {
	case answer.OK:
		if answer.Result.Stopped != "" {
			fmt.Printf("stopped the %s that held %s\n", answer.Result.Stopped, label)
		}
		fmt.Printf("moved %s to %s; it now runs there as %s::%s\n", label, answer.Result.Host.Name, answer.Result.Host.Name, session.ID)
		return nil
	case answer.Unknown:
		return fmt.Errorf("%s; no host writes to %s until it settles; see agenthail move list", answer.Error, label)
	case strings.TrimSpace(answer.Error) != "":
		return errors.New(answer.Error)
	}
	return errMoveNotDone
}

var errMoveNotDone = errors.New("the move did not finish")

func (a *App) cmdMoveList(args []string) error {
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	api, err := a.daemonAPI()
	if err != nil {
		return err
	}
	query := url.Values{}
	if hasFlag(args, "--unfinished") {
		query.Set("unfinished", "1")
	}
	var listed struct {
		Moves []struct {
			ID           string    `json:"id"`
			Conversation string    `json:"conversation"`
			PeerHost     string    `json:"peerHost"`
			PeerHostName string    `json:"peerHostName"`
			Role         string    `json:"role"`
			State        string    `json:"state"`
			Detail       string    `json:"detail"`
			UpdatedAt    time.Time `json:"updatedAt"`
		} `json:"moves"`
	}
	if err := api.getJSON(ctx, "/api/v1/session-moves", query, &listed); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(listed)
	}
	if len(listed.Moves) == 0 {
		fmt.Println("no moves")
		return nil
	}
	for _, move := range listed.Moves {
		direction := "to"
		if move.Role == "in" {
			direction = "from"
		}
		peer := move.PeerHostName
		if peer == "" {
			peer = move.PeerHost
		}
		line := fmt.Sprintf("%s  %s %s %s  %s  %s", move.UpdatedAt.Local().Format("2006-01-02 15:04"), move.Conversation, direction, peer, move.State, move.ID)
		if move.Detail != "" {
			line += "  " + move.Detail
		}
		fmt.Println(line)
	}
	return nil
}
