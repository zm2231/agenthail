package cli

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

func renderListView(state daemon.SessionViewSnapshot, wide bool) error {
	count := state.View.Counts[state.View.Scope]
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "%s: %s, %s\n", state.View.Scope, daemon.CountText(count.Families, "conversation"), daemon.CountText(count.Sessions, "session"))
	writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
	heading := "PROJECT"
	if wide {
		heading = "CWD"
	}
	fmt.Fprintf(writer, "HOST\tSURFACE\tSESSION\t%s\tSTATUS\tQUEUED\tWORKING CHILDREN\tTERMINALS\tDETAIL\n", heading)
	depth := map[string]int{}
	contextOnly := map[string]bool{}
	working := map[string]int{}
	for _, family := range state.View.Families {
		working[family.RootID] = family.WorkingSubagents
		for _, member := range family.Members {
			depth[member.ID] = member.Depth
			contextOnly[member.ID] = member.ContextOnly
		}
	}
	for _, row := range state.Sessions {
		name := row.Name
		if row.Subagent != nil && row.Subagent.Nickname != "" {
			name = row.Subagent.Nickname
		}
		if row.Alias != "" {
			name = "@" + row.Alias + " " + name
		}
		name = strings.Repeat("  ", depth[row.ID]) + name
		host := ""
		if row.Host != nil {
			host = row.Host.Name
		}
		project := "-"
		if row.Project != nil {
			project = row.Project.DisplayName
		}
		if wide {
			project = row.Cwd
		}
		detail, err := renderDisplayDetail(*row.Display)
		if err != nil {
			return err
		}
		if contextOnly[row.ID] {
			detail = strings.Trim("Parent of a match · "+detail, " ·")
		}
		if wide && row.ClaudeConfigRoot != nil {
			detail += " · " + row.ClaudeConfigRoot.Name
		}
		pins := make([]string, 0, len(row.Holders))
		for _, holder := range row.Holders {
			pin := strconv.Itoa(holder.PID)
			if holder.TTY != "" {
				pin += " " + holder.TTY
			}
			pins = append(pins, pin)
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n", listCell(host), row.Surface, listCell(name), listCell(project), listCell(row.Display.Label), row.QueueCount, working[row.ID], listCell(strings.Join(pins, ", ")), listCell(detail))
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	_, err := buffer.WriteTo(os.Stdout)
	return err
}

func listCell(value string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(value)
}

func renderDisplayDetail(display daemon.SessionDisplay) (string, error) {
	parts := []string{display.Detail}
	for _, instant := range []struct {
		label string
		at    *time.Time
	}{{"Resets", display.ResetsAt}, {"Resumes", display.ResumesAt}, {"Quiet since", display.QuietSince}, {"Tool started", display.ToolStartedAt}} {
		if instant.at != nil {
			formatted, err := localDisplayTime(*instant.at)
			if err != nil {
				return "", err
			}
			parts = append(parts, instant.label+" "+formatted)
		}
	}
	return strings.Trim(strings.Join(parts, " · "), " ·"), nil
}
