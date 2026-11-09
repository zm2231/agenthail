package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
	"github.com/zm2231/agenthail/internal/project"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
)

type baseProjectCheckout struct {
	Host      dashboardHost `json:"host"`
	ProjectID string        `json:"projectId"`
	RootPath  string        `json:"rootPath"`
	Added     bool          `json:"added"`
	Archived  bool          `json:"archived"`
}

type baseProject struct {
	ID           string                        `json:"id"`
	DisplayName  string                        `json:"displayName"`
	Kind         string                        `json:"kind"`
	Key          string                        `json:"key,omitempty"`
	Repository   *project.Repository           `json:"repository,omitempty"`
	Checkouts    []baseProjectCheckout         `json:"checkouts"`
	Coordinators []dashboardProjectCoordinator `json:"coordinators"`
	Archived     bool                          `json:"archived"`
}

type snapshotBaseContent struct {
	Daemon           map[string]any              `json:"daemon"`
	Surfaces         []dashboardSurface          `json:"surfaces"`
	Projects         []baseProject               `json:"projects"`
	Queue            []dashboardQueue            `json:"queue"`
	Channels         []dashboardChannel          `json:"channels"`
	Relays           []dashboardRelay            `json:"relays"`
	Attention        []dashboardAttention        `json:"attention"`
	DeliveryProblems []registry.DeliveryProblem  `json:"deliveryProblems"`
	CodexRecentHours int                         `json:"codexRecentHours"`
	BusyDelivery     string                      `json:"busyDelivery"`
	Signin           []signin.Status             `json:"signin"`
	Rekeyed          map[string]string           `json:"rekeyed,omitempty"`
	Coordinators     []registry.CoordinatorEntry `json:"coordinators"`
	Person           dashboardPerson             `json:"person"`
	LeadReplacements []dashboardLeadReplacement  `json:"leadReplacements"`
	DecisionsRev     string                      `json:"decisionsRev"`
}

// snapshotBase is the snapshot without sessions or anything derived from them,
// so its tag moves only when one of these fields does.
type snapshotBase struct {
	UpdatedAt   time.Time `json:"updatedAt"`
	EventCursor uint64    `json:"eventCursor"`
	HostEpoch   string    `json:"hostEpoch"`
	CatalogSeq  uint64    `json:"catalogSeq"`
	snapshotBaseContent
}

func newSnapshotBase(state dashboardState) snapshotBase {
	projects := make([]baseProject, 0, len(state.Projects))
	for _, source := range state.Projects {
		checkouts := make([]baseProjectCheckout, 0, len(source.Checkouts))
		for _, checkout := range source.Checkouts {
			checkouts = append(checkouts, baseProjectCheckout{Host: checkout.Host, ProjectID: checkout.ProjectID, RootPath: checkout.RootPath, Added: checkout.Added, Archived: checkout.Archived})
		}
		sort.Slice(checkouts, func(i, j int) bool { return checkouts[i].ProjectID < checkouts[j].ProjectID })
		projects = append(projects, baseProject{ID: source.ID, DisplayName: source.DisplayName, Kind: source.Kind, Key: source.Key, Repository: source.Repository, Checkouts: checkouts, Coordinators: source.Coordinators, Archived: source.Archived})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return snapshotBase{UpdatedAt: state.UpdatedAt, EventCursor: state.EventCursor, HostEpoch: state.HostEpoch, CatalogSeq: state.CatalogSeq, snapshotBaseContent: snapshotBaseContent{
		Daemon: state.Daemon, Surfaces: state.Surfaces, Projects: projects, Queue: state.Queue, Channels: state.Channels, Relays: state.Relays,
		Attention: state.Attention, DeliveryProblems: state.DeliveryProblems, CodexRecentHours: state.CodexRecentHours, BusyDelivery: state.BusyDelivery,
		Signin: state.Signin, Rekeyed: state.Rekeyed, Coordinators: state.Coordinators, Person: state.Person, LeadReplacements: state.LeadReplacements,
		DecisionsRev: state.DecisionsRev,
	}}
}

type projectActivityCheckout struct {
	ProjectID    string `json:"projectId"`
	SessionCount int    `json:"sessionCount"`
}

type projectActivity struct {
	ID           string                    `json:"id"`
	LastActive   time.Time                 `json:"lastActive,omitempty"`
	SessionCount int                       `json:"sessionCount"`
	Checkouts    []projectActivityCheckout `json:"checkouts"`
}

type projectActivityPage struct {
	Projects []projectActivity `json:"projects"`
}

func newProjectActivity(state dashboardState) projectActivityPage {
	page := projectActivityPage{Projects: make([]projectActivity, 0, len(state.Projects))}
	for _, source := range state.Projects {
		activity := projectActivity{ID: source.ID, LastActive: source.LastActive, SessionCount: source.SessionCount, Checkouts: make([]projectActivityCheckout, 0, len(source.Checkouts))}
		for _, checkout := range source.Checkouts {
			activity.Checkouts = append(activity.Checkouts, projectActivityCheckout{ProjectID: checkout.ProjectID, SessionCount: checkout.SessionCount})
		}
		sort.Slice(activity.Checkouts, func(i, j int) bool { return activity.Checkouts[i].ProjectID < activity.Checkouts[j].ProjectID })
		page.Projects = append(page.Projects, activity)
	}
	sort.Slice(page.Projects, func(i, j int) bool { return page.Projects[i].ID < page.Projects[j].ID })
	return page
}

func contentTag(hostEpoch string, content any) (string, error) {
	encoded, err := apitime.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return `"` + hostEpoch + "-" + hex.EncodeToString(sum[:12]) + `"`, nil
}

func writeTagged(w http.ResponseWriter, r *http.Request, tag string, body any) {
	w.Header().Set("ETag", tag)
	if r.Header.Get("If-None-Match") == tag {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeDashboardJSON(w, http.StatusOK, body)
}

func (d *Daemon) snapshotBaseHandler(dashboard *dashboardServer, w http.ResponseWriter, r *http.Request) {
	state, _, err := d.cachedDashboardState(r.Context(), dashboard, r.URL.Query().Get("fresh") == "1")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	base := newSnapshotBase(state)
	tag, err := contentTag(base.HostEpoch, base.snapshotBaseContent)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeTagged(w, r, tag, base)
}

func (d *Daemon) projectActivityHandler(dashboard *dashboardServer, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for project activity.")
		return
	}
	state, _, err := d.cachedDashboardState(r.Context(), dashboard, false)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	page := newProjectActivity(state)
	tag, err := contentTag(state.HostEpoch, page)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeTagged(w, r, tag, page)
}
