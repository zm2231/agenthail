package daemon

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/voice"
)

type voiceProjects struct{ d *Daemon }

func (p voiceProjects) List(ctx context.Context) ([]voice.Project, error) {
	return p.d.projectLeads(ctx)
}

func (p voiceProjects) LeadSession(_ context.Context, name string) (string, error) {
	spec, err := p.d.projectLead(name)
	if err != nil {
		return "", err
	}
	service := p.d.coordinator()
	if spec.HostID != "" {
		return "", fmt.Errorf("%s runs on host %s; voice can only be transferred to sessions on this host. Send it a message instead", service.Ref(spec), service.HostName(spec.HostID))
	}
	id, err := p.d.Registry.CoordinatorSessionID(spec)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("%s has no conversation yet; send it a message first, which starts one", service.Ref(spec))
	}
	return id, nil
}

func (p voiceProjects) SendToLead(ctx context.Context, name, message, sourceSessionID string) (voice.LeadReceipt, error) {
	result, err := p.d.sendToCoordinator(ctx, coordinatorRequest{Action: "coordinator-send", Project: name, Message: message, SourceSessionID: sourceSessionID})
	if err != nil {
		return voice.LeadReceipt{}, err
	}
	reply := result.Body.(coordinatorSendReply)
	return voice.LeadReceipt{Ref: reply.Ref, Started: reply.Started, Receipt: reply.Result}, nil
}
