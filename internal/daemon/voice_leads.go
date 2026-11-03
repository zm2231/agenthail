package daemon

import (
	"context"
	"fmt"
)

type voiceLeads struct{ d *Daemon }

func (p voiceLeads) LeadSession(_ context.Context, name string) (string, error) {
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
