package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/broadcast"
)

const (
	channelRuleGone      = "channel_gone"
	channelRuleNotMember = "not_a_member"
)

// channelHome is the host that keeps a channel a session took with it when it
// moved here.
func (d *Daemon) channelHome(ctx context.Context, request dashboardActionRequest) (string, string, bool, error) {
	if _, fromHost := requestingHost(ctx); fromHost || request.SourceSessionID == "" {
		return "", "", false, nil
	}
	name := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
	home, found, err := d.Registry.RemoteChannel(request.SourceSessionID, name)
	return name, home, found, err
}

// postToChannelHome posts as a moved session to a channel its former host
// keeps, holding the post while that host is unreachable.
func (d *Daemon) postToChannelHome(ctx context.Context, home, name string, request dashboardActionRequest) (actionResult, error) {
	key := actionKey(ctx)
	if key == "" {
		key = "channel-" + uuid.NewString()
	}
	post := map[string]any{"action": "channel-send", "channel": name, "message": request.Message, "sourceSessionId": request.SourceSessionID}
	target := broadcast.Target{Name: name, Channel: name}
	recipients, leg := d.channelHostLeg(ctx, home, key+":"+home, post, target)
	if leg.State == channelLegRefused && (leg.Rule == channelRuleGone || leg.Rule == channelRuleNotMember) {
		if err := d.Registry.DropRemoteChannel(request.SourceSessionID, name, home); err != nil {
			return actionResult{}, err
		}
	}
	return d.channelReply(broadcast.Result{Target: target, Recipients: recipients}, []channelHostLeg{leg})
}

// channelPostScope lets a paired host's session post to a channel here only
// while it is a member.
func (d *Daemon) channelPostScope(ctx context.Context, request dashboardActionRequest, resolved error, target broadcast.Target, source string) error {
	if _, fromHost := requestingHost(ctx); !fromHost || source == "" || target.ProjectID != "" {
		return nil
	}
	if resolved != nil {
		return &actionError{Status: http.StatusNotFound, Code: "not_found", Message: resolved.Error(), Extra: map[string]any{"detail": map[string]any{"rule": channelRuleGone}}}
	}
	members, err := d.Registry.ChannelMembers(target.Channel)
	if err != nil {
		return err
	}
	if !slices.Contains(members, d.coordinator().Canonical(source)) {
		return &actionError{Status: http.StatusForbidden, Code: "out_of_scope", Message: fmt.Sprintf("%s is not a member of #%s", source, target.Name), Extra: map[string]any{"detail": map[string]any{"rule": channelRuleNotMember}}}
	}
	return nil
}
