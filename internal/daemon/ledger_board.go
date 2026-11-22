package daemon

import (
	"slices"
	"strings"
	"time"

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

	prOpenedLog = "PR opened: "
	prMergedLog = "PR merged: "
	prClosedLog = "PR closed unmerged: "
)

var ledgerBoardLanes = []string{laneYou, laneCIFailing, laneCIPending, laneReview, laneRunning, laneBlocked, laneIdle}

type ledgerPageFlight struct {
	Lane        string          `json:"lane"`
	HolderState string          `json:"holderState,omitempty"`
	HolderSince time.Time       `json:"holderSince,omitzero"`
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
}

func (d *Daemon) ledgerBoardReads() *ledgerBoardReads {
	reads := &ledgerBoardReads{d: d, sessions: map[string]*surface.Session{}, prs: map[string]registry.SessionPR{}, asks: map[string][]registry.Ask{}}
	if bySession, err := d.Registry.SessionPRs(); err == nil {
		for _, prs := range bySession {
			for _, pr := range prs {
				if known, ok := reads.prs[pr.URL]; !ok || pr.LastReadAt.After(known.LastReadAt) {
					reads.prs[pr.URL] = pr
				}
			}
		}
	}
	if open, err := d.Registry.OpenAsks(); err == nil {
		for _, ask := range open {
			if ask.RoutedTo == registry.AskToPerson {
				reads.asks[ask.SessionID] = append(reads.asks[ask.SessionID], ask)
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

func (r *ledgerBoardReads) flight(board *ledger.Board, item ledger.WorkItem) *ledgerPageFlight {
	flight := &ledgerPageFlight{PRs: itemPRs(item, r.prs), WaitsOn: board.OpenDependencies(item.ID)}
	if item.Assignee != "" {
		flight.HolderState = holderUnknown
		if session := r.session(item.Assignee); session != nil {
			flight.HolderState, flight.HolderSince = holderState(*session), session.LastActive
		}
		for _, ask := range r.asks[item.Assignee] {
			flight.Asks = append(flight.Asks, ledgerPageAsk{ID: ask.ID, Title: firstLine(ask.Title), Since: ask.CreatedAt})
		}
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
// only by that log, never by sharing a holder.
func itemPRs(item ledger.WorkItem, watched map[string]registry.SessionPR) []ledgerPagePR {
	var order []string
	ended := map[string]string{}
	for _, entry := range item.Timeline {
		if entry.Kind != "log" {
			continue
		}
		for _, prefix := range []string{prOpenedLog, prMergedLog, prClosedLog} {
			url, ok := strings.CutPrefix(strings.TrimSpace(entry.Text), prefix)
			if !ok || url == "" || strings.ContainsAny(url, " \n") {
				continue
			}
			if !slices.Contains(order, url) {
				order = append(order, url)
			}
			switch prefix {
			case prMergedLog:
				ended[url] = "merged"
			case prClosedLog:
				ended[url] = "closed"
			}
		}
	}
	prs := make([]ledgerPagePR, 0, len(order))
	for _, url := range order {
		pr := ledgerPagePR{URL: url, State: "open"}
		if read, ok := watched[url]; ok {
			pr.Number, pr.Title, pr.Checks, pr.Review = read.Number, read.Title, read.Checks, read.Review
			if read.State != "" {
				pr.State = read.State
			}
		}
		if state, ok := ended[url]; ok {
			pr.State = state
		}
		prs = append(prs, pr)
	}
	return prs
}
