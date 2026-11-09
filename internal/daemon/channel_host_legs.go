package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	channelLegReached = "reached"
	channelLegHeld    = "held"
	channelLegUnknown = "unknown"
	channelLegRefused = "refused"
)

type channelHostLeg struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Recipients int    `json:"recipients"`
	Detail     string `json:"detail,omitempty"`
	Rule       string `json:"rule,omitempty"`
}

type channelDeliverAnswer struct {
	Recipients []broadcast.Recipient `json:"recipients"`
	Error      struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Detail struct {
		Rule string `json:"rule"`
	} `json:"detail"`
}

// A paired host's request is answered from this host only, so it never
// reaches a third host or travels back.
func (d *Daemon) channelHostLegs(ctx context.Context, target broadcast.Target, message, source string) ([]broadcast.Recipient, []channelHostLeg) {
	if _, fromHost := requestingHost(ctx); fromHost || target.ProjectID == "" {
		return nil, nil
	}
	hostIDs, err := d.channelProjectHosts(target.ProjectID)
	if err != nil {
		return nil, []channelHostLeg{{State: channelLegRefused, Detail: err.Error()}}
	}
	if len(hostIDs) == 0 {
		return nil, nil
	}
	base := actionKey(ctx)
	if base == "" {
		base = "channel-" + uuid.NewString()
	}
	template := map[string]any{"action": "channel-deliver", "projectId": target.ProjectID, "message": message}
	if source != "" {
		template["sourceSessionId"] = source
		if project := d.sourceProjectID(source); project != "" {
			template["sourceProjectId"] = project
		}
	}
	legs := make([]channelHostLeg, len(hostIDs))
	answers := make([][]broadcast.Recipient, len(hostIDs))
	var wg sync.WaitGroup
	for index, hostID := range hostIDs {
		wg.Go(func() {
			answers[index], legs[index] = d.channelHostLeg(ctx, hostID, base+":"+hostID, maps.Clone(template), target)
		})
	}
	wg.Wait()
	return slices.Concat(answers...), legs
}

func (d *Daemon) channelProjectHosts(projectID string) ([]string, error) {
	ids, err := d.Registry.CatalogProjectHosts(projectID)
	if err != nil {
		return nil, err
	}
	specs, err := d.Registry.ProjectCoordinators(projectID)
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		if spec.HostID != "" && !slices.Contains(ids, spec.HostID) {
			ids = append(ids, spec.HostID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (d *Daemon) sourceProjectID(source string) string {
	if _, pseudo := surface.AgenthailSender(source, ""); pseudo {
		return ""
	}
	session, err := d.Registry.Session(source)
	if err != nil {
		return ""
	}
	project, err := d.sessionProject(*session)
	if err != nil || project == nil {
		return ""
	}
	return project.ID
}

func (d *Daemon) channelHostLeg(ctx context.Context, hostID, key string, request map[string]any, target broadcast.Target) ([]broadcast.Recipient, channelHostLeg) {
	leg := channelHostLeg{ID: hostID, Name: hostID}
	host, paired := d.pairedHost(hostID)
	if !paired {
		leg.State, leg.Detail = channelLegRefused, "host is no longer paired"
		return nil, leg
	}
	leg.Name = host.record.Name
	if _, err := d.routeSource(ctx, request, host); err != nil {
		leg.State, leg.Detail = channelLegRefused, err.Error()
		return nil, leg
	}
	body, err := json.Marshal(request)
	if err != nil {
		leg.State, leg.Detail = channelLegRefused, err.Error()
		return nil, leg
	}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, body, http.Header{actionIdempotencyHeader: []string{key}})
	var answer hostAnswer
	if err == nil {
		answer, err = readHostAnswer(host, response)
	}
	if err == nil && answer.status != http.StatusAccepted {
		return channelLegAnswer(leg, answer)
	}
	var notSent hosts.NotSentError
	sent := !errors.As(err, &notSent)
	if _, holdErr := d.Registry.HoldHostAction(hostID, key, "#"+target.Label(), body, sent); holdErr != nil {
		leg.State, leg.Detail = channelLegRefused, errors.Join(err, holdErr).Error()
		return nil, leg
	}
	d.refreshHostQueueCounts()
	leg.State, leg.Detail = channelLegHeld, fmt.Sprintf("host %s is unreachable; held until it reconnects", host.record.Name)
	if sent {
		leg.State, leg.Detail = channelLegUnknown, fmt.Sprintf("host %s did not answer; held until it confirms", host.record.Name)
	}
	return nil, leg
}

func channelLegAnswer(leg channelHostLeg, answer hostAnswer) ([]broadcast.Recipient, channelHostLeg) {
	var reply channelDeliverAnswer
	_ = json.Unmarshal(answer.payload, &reply)
	if answer.status == http.StatusOK || answer.status == http.StatusConflict && reply.Error.Code == "conflict" {
		for index := range reply.Recipients {
			reply.Recipients[index].HostID = leg.ID
			if name := reply.Recipients[index].Coordinator; name != "" {
				reply.Recipients[index].Coordinator = leg.ID + "::" + name
			}
		}
		leg.State, leg.Recipients = channelLegReached, len(reply.Recipients)
		return reply.Recipients, leg
	}
	leg.State, leg.Rule = channelLegRefused, reply.Detail.Rule
	leg.Detail = reply.Error.Message
	if leg.Detail == "" {
		leg.Detail = fmt.Sprintf("answered %d", answer.status)
	}
	return nil, leg
}
