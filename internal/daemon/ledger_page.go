package daemon

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/apitime"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sse"
)

const (
	ledgerPageDone     = 20
	ledgerPageLineCap  = 200
	ledgerPageDoneDays = 30
)

//go:embed ledger_page
var ledgerPageFiles embed.FS

var ledgerPageStates = []ledger.WorkState{ledger.WorkInProgress, ledger.WorkInReview, ledger.WorkBlocked, ledger.WorkOpen, ledger.WorkParked}

type ledgerPageView struct {
	Project string                     `json:"project"`
	Key     string                     `json:"key,omitempty"`
	Work    map[string][]ledgerPageRow `json:"work"`
	Waiting []ledgerPageWait           `json:"waiting"`
	Done    []ledgerPageRow            `json:"done"`
}

type ledgerPageRow struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	What     string            `json:"what,omitempty"`
	For      string            `json:"for,omitempty"`
	Type     string            `json:"type,omitempty"`
	Priority string            `json:"priority,omitempty"`
	Holder   string            `json:"holder,omitempty"`
	Since    time.Time         `json:"since,omitzero"`
	Note     string            `json:"note,omitempty"`
	Reviewer string            `json:"reviewer,omitempty"`
	Parked   *ledgerPageParked `json:"parked,omitempty"`
}

type ledgerPageParked struct {
	Kind    string    `json:"kind"`
	Why     string    `json:"why,omitempty"`
	Revisit time.Time `json:"revisit,omitzero"`
}

type ledgerPageWait struct {
	ID             string         `json:"id"`
	Question       string         `json:"question"`
	Recommendation string         `json:"recommendation,omitempty"`
	From           string         `json:"from,omitempty"`
	Deadline       time.Time      `json:"deadline,omitzero"`
	Work           *ledgerPageRow `json:"work,omitempty"`
}

func (d *Daemon) ledgerPage() http.Handler {
	files, err := fs.Sub(ledgerPageFiles, "ledger_page")
	if err != nil {
		panic(err)
	}
	static := http.FileServerFS(files)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ledger", d.ledgerPageDataHandler)
	mux.HandleFunc("GET /events", d.ledgerPageEventsHandler)
	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	})
	return mux
}

// The page takes any id the dashboard and apps list a project under: the
// project's own id, or a checkout's local id, bare or qualified by its host.
// It reads the ledger the sessions in that project write to.
func (d *Daemon) ledgerPageTarget(projectID string) (string, error) {
	if host, local, qualified := hosts.Split(projectID); qualified && host == d.hostIdentity().ID {
		projectID = local
	}
	found, err := d.ledgerPageProject(projectID)
	if err != nil {
		return "", err
	}
	return LedgerKey(d.Registry, found)
}

func (d *Daemon) ledgerPageProject(projectID string) (*CatalogProject, error) {
	links, err := loadProjectLinks(d.Registry)
	if err != nil {
		return nil, err
	}
	raw, err := d.Registry.CatalogCheckoutProject(projectID)
	if err != nil {
		return nil, err
	}
	var found CatalogProject
	if len(raw) > 0 && json.Unmarshal(raw, &found) == nil && found.ID != "" {
		return links.apply(&found), nil
	}
	coordinators, err := d.Registry.LocalCoordinators()
	if err != nil {
		return nil, err
	}
	for _, spec := range coordinators {
		if lead := coordinatorCatalogProject(spec); lead.LocalID == projectID {
			return links.apply(lead), nil
		}
	}
	return &CatalogProject{ID: projectID, Key: projectID}, nil
}

// A project with nothing recorded yet reads as an empty ledger, not an error.
func (d *Daemon) ledgerPageKey(projectID string) (string, error) {
	key, err := d.ledgerPageTarget(projectID)
	if err != nil || key == "" {
		return "", err
	}
	keys, err := d.ledgerStore().ProjectKeys()
	if err != nil || !slices.Contains(keys, key) {
		return "", err
	}
	return key, nil
}

func (d *Daemon) ledgerPageDataHandler(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project"))
	if projectID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "project is required.")
		return
	}
	key, err := d.ledgerPageKey(projectID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "ledger_unavailable", err.Error())
		return
	}
	view := ledgerPageView{Project: projectID, Key: key, Work: map[string][]ledgerPageRow{}, Waiting: []ledgerPageWait{}, Done: []ledgerPageRow{}}
	for _, state := range ledgerPageStates {
		view.Work[string(state)] = []ledgerPageRow{}
	}
	if key != "" {
		board, err := d.ledgerStore().Board(key)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "ledger_unavailable", err.Error())
			return
		}
		d.fillLedgerPage(&view, board, time.Now())
	}
	body, err := apitime.Marshal(view)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "encode_failed", err.Error())
		return
	}
	etag := ledgerETag(body)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if ledgerETagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (d *Daemon) fillLedgerPage(view *ledgerPageView, board *ledger.Board, now time.Time) {
	names := map[string]string{}
	name := func(sessionID string) string {
		if sessionID == "" {
			return ""
		}
		if known, ok := names[sessionID]; ok {
			return known
		}
		names[sessionID] = d.resolveDisplay(sessionID)
		return names[sessionID]
	}
	row := func(item ledger.WorkItem) ledgerPageRow {
		out := ledgerPageRow{ID: item.ID, Title: item.Text, What: firstLine(item.Description), Type: string(item.Type), Priority: item.Priority, Holder: name(item.Assignee), Since: stateSince(item)}
		if clause, ok := board.Clause(item.ClauseID); ok && clause.Text != item.Text {
			out.For = firstLine(clause.Text)
		}
		return out
	}
	var done []ledgerPageRow
	for _, item := range board.WorkItems() {
		switch item.State {
		case ledger.WorkDone:
			if r := row(item); now.Sub(r.Since) <= ledgerPageDoneDays*24*time.Hour {
				r.Note, r.Reviewer = firstLine(item.Reason), name(item.Reviewer)
				done = append(done, r)
			}
		case ledger.WorkBlocked:
			r := row(item)
			r.Note = firstLine(item.Reason)
			view.Work[string(item.State)] = append(view.Work[string(item.State)], r)
		case ledger.WorkParked:
			r := row(item)
			if item.Park == nil {
				view.Work[string(item.State)] = append(view.Work[string(item.State)], r)
				continue
			}
			if item.Park.Kind == ledger.ParkPerson && item.Park.Person != nil {
				view.Waiting = append(view.Waiting, ledgerPageWait{ID: item.ID, Question: item.Park.Person.Question, From: name(item.Park.Session), Work: &r})
				continue
			}
			r.Parked = &ledgerPageParked{Kind: string(item.Park.Kind), Why: firstLine(item.Park.Why), Revisit: item.Park.Revisit}
			view.Work[string(item.State)] = append(view.Work[string(item.State)], r)
		case ledger.WorkInProgress, ledger.WorkInReview, ledger.WorkOpen:
			view.Work[string(item.State)] = append(view.Work[string(item.State)], row(item))
		}
	}
	for state, rows := range view.Work {
		if state == string(ledger.WorkOpen) {
			slices.SortStableFunc(rows, byPriorityThenID)
			continue
		}
		slices.SortStableFunc(rows, newestFirst)
	}
	slices.SortStableFunc(done, newestFirst)
	view.Done = append(view.Done, done[:min(len(done), ledgerPageDone)]...)
	for _, question := range board.Questions() {
		if question.State != ledger.QuestionOpen {
			continue
		}
		view.Waiting = append(view.Waiting, ledgerPageWait{ID: question.ID, Question: question.Text, Recommendation: question.Recommendation, From: name(question.OwnerSessionID), Deadline: time.UnixMilli(question.DeadlineMs).UTC()})
	}
	slices.SortStableFunc(view.Waiting, func(a, b ledgerPageWait) int { return strings.Compare(a.ID, b.ID) })
}

func stateSince(item ledger.WorkItem) time.Time {
	for i := len(item.Timeline) - 1; i >= 0; i-- {
		if entry := item.Timeline[i]; entry.State != "" {
			return entry.At
		}
	}
	return time.Time{}
}

func newestFirst(a, b ledgerPageRow) int {
	if c := b.Since.Compare(a.Since); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func byPriorityThenID(a, b ledgerPageRow) int {
	if a.Priority != b.Priority {
		if a.Priority == "" {
			return 1
		}
		if b.Priority == "" {
			return -1
		}
		return strings.Compare(a.Priority, b.Priority)
	}
	return compareWorkIDs(a.ID, b.ID)
}

func compareWorkIDs(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) <= ledgerPageLineCap {
		return line
	}
	runes := []rune(line)[:ledgerPageLineCap]
	cut := string(runes)
	if space := strings.LastIndexByte(cut, ' '); space > ledgerPageLineCap/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

func (d *Daemon) ledgerPageEventsHandler(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project"))
	if projectID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "project is required.")
		return
	}
	key, err := d.ledgerPageTarget(projectID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "ledger_unavailable", err.Error())
		return
	}
	if key == "" {
		key = registry.LedgerProjectKey(projectID)
	}
	hub := d.ledgerChanges()
	id, wake, cancel := hub.subscribe(key)
	defer cancel()
	sse.Open(w)
	if sse.Flush(w) != nil {
		return
	}
	authorized := func() bool { return d.apiEventStreamAuthorized(r) }
	sse.Follow(r.Context(), w, wake, func(_ struct{}, open bool) bool {
		if !open || !authorized() {
			return false
		}
		for _, change := range hub.take(id) {
			writeLedgerChange(w, change)
		}
		return sse.Flush(w) == nil
	}, authorized)
}
