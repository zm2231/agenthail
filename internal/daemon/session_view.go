package daemon

import "time"

type SessionViewCount struct {
	Families int `json:"families"`
	Sessions int `json:"sessions"`
}

type SessionViewFilters struct {
	ProjectID  string `json:"projectId,omitempty"`
	Query      string `json:"q,omitempty"`
	Surface    string `json:"surface,omitempty"`
	Status     string `json:"status,omitempty"`
	Attention  bool   `json:"attention,omitempty"`
	SelectedID string `json:"selectedId,omitempty"`
	Collapsed  bool   `json:"collapsed,omitempty"`
}

type SessionViewMember struct {
	ID          string `json:"id"`
	Depth       int    `json:"depth"`
	ContextOnly bool   `json:"contextOnly"`
	Pinned      bool   `json:"pinned"`
	Worker      bool   `json:"worker,omitempty"`
}

type SessionViewWorkers struct {
	Count    int `json:"count"`
	Working  int `json:"working"`
	NeedsYou int `json:"needsYou"`
}

type SessionViewFamily struct {
	ID               string              `json:"id"`
	RootID           string              `json:"rootId"`
	ProjectID        string              `json:"projectId"`
	CheckoutID       string              `json:"checkoutId"`
	Members          []SessionViewMember `json:"members"`
	Pinned           bool                `json:"pinned"`
	WorkingSubagents int                 `json:"workingSubagents"`
	Workers          *SessionViewWorkers `json:"workers,omitempty"`
	activity         time.Time
	key              sessionViewKey
}

type SessionViewProject struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	Counts             SessionViewCount `json:"counts"`
	CollapsedFamilyIDs []string         `json:"collapsedFamilyIds"`
	active             bool
}

type SessionViewSummary struct {
	QueuedMessages    int `json:"queuedMessages"`
	AttentionFamilies int `json:"attentionFamilies"`
}

type SessionViewSurface struct {
	Surface string                      `json:"surface"`
	Counts  map[string]SessionViewCount `json:"counts"`
	Summary SessionViewSummary          `json:"summary"`
}

type SessionView struct {
	CatalogHostID string                      `json:"catalogHostId"`
	Revision      string                      `json:"revision"`
	AsOf          time.Time                   `json:"asOf"`
	RefreshAt     *time.Time                  `json:"refreshAt"`
	Scope         string                      `json:"scope"`
	Filters       SessionViewFilters          `json:"filters"`
	Counts        map[string]SessionViewCount `json:"counts"`
	Summary       SessionViewSummary          `json:"summary"`
	Surfaces      []SessionViewSurface        `json:"surfaces"`
	Projects      []SessionViewProject        `json:"projects"`
	Families      []SessionViewFamily         `json:"families"`
}

type SessionAttention struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// AttentionReplacedRequest is an open request of a conversation that is no
// longer its project's lead; it is shown apart and does not count as needing the person.
const AttentionReplacedRequest = "replacedRequest"

type PresentationPosition struct {
	HostID   string `json:"hostId"`
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
}

type SessionViewSnapshot struct {
	View                 *SessionView         `json:"view"`
	PresentationPosition PresentationPosition `json:"presentationPosition"`
	HostEpoch            string               `json:"hostEpoch"`
	CatalogSeq           uint64               `json:"catalogSeq"`
	Sessions             []dashboardSession   `json:"sessions"`
	NextCursor           string               `json:"nextCursor,omitempty"`
	TotalSessions        int                  `json:"totalSessions"`
	Counts               map[string]int       `json:"counts"`
}
