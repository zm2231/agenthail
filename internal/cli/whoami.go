package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/agentcard"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdWhoami(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail whoami [--json]")
	}
	card, err := a.callerCard()
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(card)
	}
	if !card.Resolved {
		fmt.Printf("unresolved: %s\n", card.Reason)
		return nil
	}
	fields := [][2]string{{"session", card.Session}, {"surface", string(card.Surface)}, {"runtime", string(card.Runtime)}}
	if card.Alias != "" {
		fields = append(fields, [2]string{"alias", "@" + card.Alias})
	}
	if card.Host != "" {
		fields = append(fields, [2]string{"host", card.Host})
	}
	if card.Project != nil {
		fields = append(fields, [2]string{"project", card.Project.Name + " (" + card.Project.ID + ")"})
	}
	fields = append(fields, [2]string{"cwd", card.Cwd})
	if card.Lead != nil {
		fields = append(fields, [2]string{"lead", card.Lead.Ref})
	}
	if card.StartedBy != nil {
		fields = append(fields, [2]string{"started by", startedByLabel(*card.StartedBy)})
	}
	if card.ReportTo != "" {
		fields = append(fields, [2]string{"report to", card.ReportTo})
	}
	for _, field := range fields {
		fmt.Printf("%s: %s\n", field[0], field[1])
	}
	return nil
}

func (a *App) cmdHelpAgent(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: agenthail help agent")
	}
	card, err := a.callerCard()
	if err != nil {
		return err
	}
	fmt.Print(agentcard.Render(card))
	return nil
}

func startedByLabel(origin agentcard.Origin) string {
	switch {
	case origin.Ref != "":
		return origin.Ref
	case origin.Name != "":
		return origin.Name
	}
	return string(origin.Via)
}

// callerCard is the card of the session this command runs for. A caller
// Agenthail cannot name is a result, not an error, so an agent can always ask.
func (a *App) callerCard() (agentcard.Card, error) {
	id, err := a.sourceSessionID(context.Background(), "")
	if err != nil {
		return agentcard.Unresolved(err.Error()), nil
	}
	if id == "" {
		return agentcard.Unresolved("no caller session binding; set AGENTHAIL_SESSION_ID, CODEX_THREAD_ID, or CLAUDE_CODE_SESSION_ID"), nil
	}
	if a.registry() == nil {
		return agentcard.Unresolved("caller session context is unavailable; run through the Agenthail CLI with its session registry"), nil
	}
	session, err := a.registry().Session(id)
	if err != nil {
		return agentcard.Unresolved(fmt.Sprintf("caller session %q is not registered; run agenthail list from the caller session", id)), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	card, err := a.cardSources().For(ctx, *session)
	if err != nil {
		return agentcard.Card{}, fmt.Errorf("describe session %s: %w", session.ID, err)
	}
	return card, nil
}

func (a *App) cardSources() agentcard.Sources {
	return agentcard.Sources{
		Registry: a.registry(),
		Project: func(ctx context.Context, cwd string) (*agentcard.Project, error) {
			found, err := daemon.ResolvePathProject(ctx, a.registry(), cwd)
			if err != nil || found == nil {
				return nil, err
			}
			return &agentcard.Project{ID: found.ID, Name: found.DisplayName, Root: found.RootPath}, nil
		},
		LeadRef:     a.coordinator().Ref,
		PromptsHeld: claudePromptsHeld,
	}
}

// claudePromptsHeld reports whether the prompt hook is installed in the
// config root a terminal Claude session lives in, or in every root when its
// transcript does not say which.
func claudePromptsHeld(session surface.Session) bool {
	home, _ := os.UserHomeDir()
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil || len(roots) == 0 {
		return false
	}
	for _, root := range roots {
		if session.Transcript != "" && strings.HasPrefix(session.Transcript, filepath.Join(root, "projects")+string(filepath.Separator)) {
			roots = []string{root}
			break
		}
	}
	for _, root := range roots {
		state, err := claudePromptHookState(filepath.Join(root, "settings.json"))
		if err != nil || state != claudePromptHookInstalled {
			return false
		}
	}
	return true
}
