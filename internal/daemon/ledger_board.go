package daemon

import (
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	laneYou       = "you"
	laneCIFailing = "ciFailing"
	laneCIPending = "ciPending"
	laneReview    = "review"
	laneRunning   = "running"
	laneBlocked   = "blocked"
	laneIdle      = "idle"

	holderWorking = "working"
	holderIdle    = "idle"
	holderEnded   = "ended"
	holderUnknown = "unknown"
)

var ledgerBoardLanes = []string{laneYou, laneCIFailing, laneCIPending, laneReview, laneBlocked, laneRunning, laneIdle}

type ledgerPageFlight struct {
	Lane        string          `json:"lane"`
	HolderState string          `json:"holderState,omitempty"`
	PRs         []ledgerPagePR  `json:"prs,omitempty"`
	Asks        []ledgerPageAsk `json:"asks,omitempty"`
	LastLog     *ledgerPageLog  `json:"lastLog,omitempty"`
	WaitsOn     []string        `json:"waitsOn,omitempty"`
}

type ledgerPagePR struct {
	URL    string `json:"url"`
	Number int    `json:"number,omitempty"`
	Title  string `json:"title,omitempty"`
	State  string `json:"state,omitempty"`
	Checks string `json:"checks,omitempty"`
	Review string `json:"review,omitempty"`
}

type ledgerPageAsk struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Since time.Time `json:"since,omitzero"`
}

type ledgerPageLog struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type ledgerBoardReads struct {
	d        *Daemon
	sessions map[string]*surface.Session
	prs      map[string]registry.SessionPR
	asks     map[string][]registry.Ask
	onItem   map[string][]registry.Ask
	toPerson map[string]bool
	hostID   string
}

func (d *Daemon) linkedPRs() map[string]registry.SessionPR {
	byURL := map[string]registry.SessionPR{}
	bySession, err := d.Registry.SessionPRs()
	if err != nil {
		return byURL
	}
	for _, prs := range bySession {
		for _, pr := range prs {
			if known, ok := byURL[pr.URL]; !ok || pr.LastReadAt.After(known.LastReadAt) {
				byURL[pr.URL] = pr
			}
		}
	}
	return byURL
}

func (d *Daemon) ledgerBoardReads() *ledgerBoardReads {
	reads := &ledgerBoardReads{d: d, sessions: map[string]*surface.Session{}, prs: d.linkedPRs(), asks: map[string][]registry.Ask{}, onItem: map[string][]registry.Ask{}, toPerson: map[string]bool{}, hostID: d.hostIdentity().ID}
	if open, err := d.Registry.OpenAsks(); err == nil {
		for _, ask := range open {
			if ask.RoutedTo == registry.AskToPerson {
				reads.toPerson[ask.ID] = true
				if ask.WorkItem != "" {
					on := ask.LedgerKey + "\x00" + ask.WorkItem
					reads.onItem[on] = append(reads.onItem[on], ask)
				} else {
					reads.asks[ask.SessionID] = append(reads.asks[ask.SessionID], ask)
				}
			}
		}
	}
	return reads
}

func (r *ledgerBoardReads) session(id string) *surface.Session {
	if id == "" {
		return nil
	}
	if session, ok := r.sessions[id]; ok {
		return session
	}
	session, err := r.d.Registry.Session(id)
	if err != nil {
		session = nil
	}
	r.sessions[id] = session
	return session
}

// An ask linked to an item marks only that item, whoever holds the item now and whether or not its question has reached the ledger yet; a holder's ask linked to no item marks every item it holds. An ask with this host's lead is not the person's.
func (r *ledgerBoardReads) flight(key string, board *ledger.Board, item ledger.WorkItem) *ledgerPageFlight {
	flight := &ledgerPageFlight{PRs: itemPRs(item, r.prs), WaitsOn: board.OpenDependencies(item.ID)}
	listed := map[string]bool{}
	if item.Assignee != "" {
		flight.HolderState = holderUnknown
		if session := r.session(item.Assignee); session != nil {
			flight.HolderState = holderState(*session)
		}
	}
	for _, ask := range append(r.asks[item.Assignee], r.onItem[key+"\x00"+item.ID]...) {
		listed[ask.ID] = true
		flight.Asks = append(flight.Asks, ledgerPageAsk{ID: ask.ID, Title: firstLine(ask.Title), Since: ask.CreatedAt})
	}
	for _, question := range item.OpenQuestions() {
		if listed[question.AskID] || question.AskHostID == r.hostID && !r.toPerson[question.AskID] {
			continue
		}
		listed[question.AskID] = true
		flight.Asks = append(flight.Asks, ledgerPageAsk{ID: question.AskID, Title: firstLine(question.Title), Since: question.At})
	}
	if len(flight.Asks) > 0 {
		flight.WaitsOn = append(flight.WaitsOn, "person")
	}
	if entry := item.LatestLog(); entry != nil {
		flight.LastLog = &ledgerPageLog{Text: firstLine(entry.Text), At: entry.At}
	}
	flight.Lane = boardLane(item, flight)
	return flight
}

func holderState(session surface.Session) string {
	switch {
	case session.Ended:
		return holderEnded
	case session.Status == surface.StatusBusy:
		return holderWorking
	case session.Status == surface.StatusIdle || session.Status == surface.StatusNotLoaded:
		return holderIdle
	}
	return holderUnknown
}

func boardLane(item ledger.WorkItem, flight *ledgerPageFlight) string {
	openChecks := func(checks string) bool {
		return slices.ContainsFunc(flight.PRs, func(pr ledgerPagePR) bool { return pr.State == "open" && pr.Checks == checks })
	}
	switch {
	case len(flight.Asks) > 0:
		return laneYou
	case openChecks("failing"):
		return laneCIFailing
	case openChecks("pending"):
		return laneCIPending
	case item.State == ledger.WorkInReview:
		return laneReview
	case item.State == ledger.WorkBlocked || len(flight.WaitsOn) > 0:
		return laneBlocked
	case flight.HolderState == holderWorking:
		return laneRunning
	}
	return laneIdle
}

// itemPRs lists the pull requests an item's own log names, in the order they
// were opened, with what this host last read of each. A PR is tied to an item
// only by that log, never by sharing a holder. Any holder can write any log
// line, so a URL counts only when this host has that PR linked: linkPR records
// the PR from a parsed GitHub reference before it writes the line.
func itemPRs(item ledger.WorkItem, linked map[string]registry.SessionPR) []ledgerPagePR {
	var order []string
	ended := map[string]string{}
	for _, entry := range item.Timeline {
		if entry.Kind != "log" {
			continue
		}
		mark, url, ok := ledger.ParsePRLine(entry.Text)
		if _, known := linked[url]; !ok || !known {
			continue
		}
		if !slices.Contains(order, url) {
			order = append(order, url)
		}
		switch mark {
		case ledger.PRMerged:
			ended[url] = "merged"
		case ledger.PRClosed:
			ended[url] = "closed"
		}
	}
	prs := make([]ledgerPagePR, 0, len(order))
	for _, url := range order {
		read := linked[url]
		pr := ledgerPagePR{URL: url, Number: read.Number, Title: read.Title, State: "open", Checks: read.Checks, Review: read.Review}
		if read.State != "" {
			pr.State = read.State
		}
		if state, ok := ended[url]; ok {
			pr.State = state
		}
		prs = append(prs, pr)
	}
	return prs
}

func (d *Daemon) ledgerBoardVersion(projectID string) string {
	view, err := d.projectLedgerPage(projectID)
	if err != nil {
		return ""
	}
	body, err := apitime.Marshal(view.Board)
	if err != nil {
		return ""
	}
	return ledgerETag(body)
}
