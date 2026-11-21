package surface

type AppServerMode string

const (
	AppServerShared  AppServerMode = "shared"
	AppServerPrivate AppServerMode = "private"
)

// Shared means Desktop drives Codex's app-server daemon, the one Agenthail dials.
type AppServer struct {
	Mode      AppServerMode `json:"mode,omitempty"`
	HostID    string        `json:"hostId,omitempty"`
	Version   string        `json:"version,omitempty"`
	Transport string        `json:"transport,omitempty"`
}
