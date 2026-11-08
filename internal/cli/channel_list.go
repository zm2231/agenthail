package cli

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/hosts"
)

type channelMemberJSON struct {
	SessionID string `json:"sessionId"`
	Display   string `json:"display"`
	HostID    string `json:"hostId,omitempty"`
}

type channelJSON struct {
	Name    string              `json:"name"`
	Members []channelMemberJSON `json:"members"`
}

func (a *App) channelList(jsonOut bool) error {
	channels, err := a.registry().ListChannels()
	if err != nil {
		return err
	}
	if jsonOut {
		out := make([]channelJSON, 0, len(channels))
		for _, ch := range channels {
			members := make([]channelMemberJSON, 0, len(ch.Members))
			for _, id := range ch.Members {
				host, err := a.registry().SessionHost(id)
				if err != nil {
					return err
				}
				if host != "" {
					id = hosts.Qualify(host, id)
				}
				members = append(members, channelMemberJSON{SessionID: id, Display: a.sessionDisplay(id), HostID: host})
			}
			out = append(out, channelJSON{Name: ch.Name, Members: members})
		}
		return emit(out)
	}
	if len(channels) == 0 {
		fmt.Println("(no channels; use: agenthail channel create <name>)")
		return nil
	}
	for _, ch := range channels {
		fmt.Printf("#%s  (%d members)\n", ch.Name, ch.MemberCount)
		for _, m := range ch.Members {
			fmt.Printf("    %s\n", a.sessionDisplay(m))
		}
	}
	return nil
}
