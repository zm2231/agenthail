package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/hosts"
)

const (
	channelRuleGone      = "channel_gone"
	channelRuleNotMember = "not_a_member"
)

// channelDestination names the channel a post goes to and the paired host
// that keeps it, or no host for one of this host's. A name qualified with a
// host picks that host's channel; a bare name that names channels on more
// than one host is refused with their qualified names.
func (d *Daemon) channelDestination(ctx context.Context, request dashboardActionRequest) (string, string, error) {
	hostRef, name, qualified := splitChannelRef(request.Channel)
	if _, fromHost := requestingHost(ctx); fromHost || qualified && d.isLocalHostRef(hostRef) {
		return name, "", nil
	}
	if qualified {
		host, paired := d.pairedHost(hostRef)
		if !paired {
			return "", "", fmt.Errorf("no paired host is named %q", hostRef)
		}
		homes, err := d.channelHomes(request.SourceSessionID, name)
		if err != nil {
			return "", "", err
		}
		if !slices.Contains(homes, host.record.ID) {
			return "", "", fmt.Errorf("%s is not in #%s on host %s", d.resolveDisplay(request.SourceSessionID), name, host.record.Name)
		}
		return name, host.record.ID, nil
	}
	homes, err := d.channelHomes(request.SourceSessionID, name)
	if err != nil || len(homes) == 0 {
		return name, "", err
	}
	var choices []string
	if _, err := broadcast.Resolve(d.Registry, name); err == nil {
		choices = append(choices, d.hostIdentity().Name+"::#"+name)
	}
	if len(homes) == 1 && len(choices) == 0 {
		return name, homes[0], nil
	}
	for _, home := range homes {
		label := home
		if host, paired := d.pairedHost(home); paired {
			label = host.record.Name
		}
		choices = append(choices, label+"::#"+name)
	}
	return "", "", fmt.Errorf("#%s names channels on more than one host: %s; name one of them", name, strings.Join(choices, ", "))
}

func (d *Daemon) channelHomes(session, name string) ([]string, error) {
	if session == "" {
		return nil, nil
	}
	return d.Registry.RemoteChannelHomes(session, name)
}

func splitChannelRef(ref string) (string, string, bool) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	host, name, qualified := hosts.Split(ref)
	return host, strings.TrimPrefix(name, "#"), qualified
}

// localChannelRef is ref with this host's qualifier taken off, so scope
// checks see the channel a local post goes to.
func (d *Daemon) localChannelRef(ref string) string {
	if host, name, qualified := splitChannelRef(ref); qualified && d.isLocalHostRef(host) {
		return name
	}
	return ref
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
	return d.channelReply(broadcast.Result{Target: target, Recipients: recipients}, []channelHostLeg{leg}, nil)
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
