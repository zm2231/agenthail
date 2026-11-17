package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	dismissDecisionAction = "dismiss-decision"
	dismissedParkWhy      = "dismissed by the person"
	dismissedDenial       = "The person dismissed this without answering"
)

var errNotThePersonsDismissal = actionFailed(http.StatusForbidden, "forbidden", "only the person dismisses a decision")

// A paired host forwards a session's action under the actor "session <id>".
func dismissesForThePerson(p principal) bool {
	switch p.Kind {
	case principalSession:
		return false
	case principalHost:
		return !strings.HasPrefix(p.Claimed, "session ")
	}
	return true
}

func reportedPark(request map[string]any) bool {
	action, _ := request["action"].(string)
	workItem, _ := request["workItem"].(string)
	_, named := request["host"]
	return action == dismissDecisionAction && strings.TrimSpace(workItem) != "" && named
}

// A parked card lives on the computer whose ledger reported it, whoever parked it; the receiver reads host "" as itself.
func (d *Daemon) routeReportedPark(request map[string]any) (*pairedHost, bool, error) {
	hostRef, _ := request["host"].(string)
	hostRef = strings.TrimSpace(hostRef)
	if hostRef == "" || d.isLocalHostRef(hostRef) {
		delete(request, "host")
		return nil, true, nil
	}
	host, found := d.pairedHost(hostRef)
	if !found {
		return nil, false, fmt.Errorf("%w %q", errUnknownHost, hostRef)
	}
	request["host"] = ""
	return host, true, nil
}

func (d *Daemon) doDismissDecision(ctx context.Context, p principal, request dashboardActionRequest) (actionResult, error) {
	if !dismissesForThePerson(p) {
		return actionResult{}, errNotThePersonsDismissal
	}
	result := map[string]any{"outcome": "dismissed"}
	var err error
	switch workItem, requestID := strings.TrimSpace(request.WorkItem), strings.TrimSpace(request.RequestID); {
	case workItem != "":
		var told bool
		told, err = d.dismissParked(workItem)
		result["parkerTold"] = told
	case requestID != "":
		err = d.dismissRequest(ctx, p, strings.TrimSpace(request.SessionID), requestID)
	default:
		err = d.dismissStopped(ctx, strings.TrimSpace(request.SessionID))
	}
	if err != nil {
		return actionResult{}, err
	}
	d.decisions.poke()
	return actionOK(map[string]any{"ok": true, "result": result}), nil
}

func decisionStateKey(item decisionItem) string {
	return item.Kind + "\x1f" + item.Reason + "\x1f" + strconv.FormatInt(item.Since.UnixMilli(), 10)
}

func (s decisionSources) dismissedNow(item decisionItem) bool {
	key, dismissed := s.dismissed[item.SessionID]
	return dismissed && key == decisionStateKey(item)
}

func (d *Daemon) decisionDismissals() map[string]string {
	dismissed, err := d.Registry.DecisionDismissals()
	if err != nil {
		d.log.Printf("decisions: read dismissals: %s", err)
	}
	return dismissed
}

func (d *Daemon) dismissStopped(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return invalidRequest("sessionId is required")
	}
	rows, _, err := d.decisionRows(ctx)
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	queued, err := d.queuedBehind()
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	page := buildDecisions(decisionSources{rows: rows, hearer: d.decisionHearer, queued: queued, requests: func(asking []dashboardSession) map[string]decisionRequests {
		return d.readDecisionRequests(ctx, asking)
	}})
	at := slices.IndexFunc(page.Decisions, func(item decisionItem) bool {
		return item.Group == decisionStopped && item.SessionID == sessionID && item.WorkItem == ""
	})
	if at < 0 {
		return staleRequest()
	}
	if err := d.Registry.DismissDecision(sessionID, decisionStateKey(page.Decisions[at]), time.Now()); err != nil {
		return actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	return nil
}

func (d *Daemon) dismissRequest(ctx context.Context, p principal, sessionID, requestID string) error {
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return actionFailed(http.StatusNotFound, "not_found", "session not found")
	}
	ask, found, err := d.Registry.Ask(requestID)
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !isAskID(requestID) || !found || ask.SessionID != session.ID || ask.State != registry.AskOpen {
		return staleRequest()
	}
	switch ask.Origin {
	case registry.AskOriginPermission:
		return d.denyForThePerson(ctx, session, ask, p.actor())
	case registry.AskOriginDelivery:
		if offersDeliveryDismiss(ask) {
			_, err := d.answerDelivery(ctx, ask, surface.Response{Choices: []string{registry.DeliveryDismiss}}, registry.AskToPerson, p.actor())
			return err
		}
	case registry.AskOriginExtensionGrant:
		_, err := d.answerExtensionGrant(ctx, ask, surface.Response{Choices: []string{extensionGrantNo}}, p, registry.AskToPerson)
		return err
	case registry.AskOriginApproval:
		_, err := d.answerApproval(ctx, ask, surface.Response{Choices: []string{registry.ApprovalDeny}}, p, registry.AskToPerson)
		return err
	}
	remote, err := d.remotePersonParks(ask.ID)
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "ledger_error", err.Error())
	}
	toldHere := false
	for _, park := range remote {
		told, err := d.forwardParkDismissal(ctx, p, park)
		if err != nil {
			return err
		}
		toldHere = toldHere || !told
	}
	if err := d.withdrawAskForThePerson(ask); err != nil {
		return err
	}
	if len(remote) > 0 {
		if toldHere {
			d.tellParkDismissed(personParked{key: remote[0].key, item: remote[0].item})
		}
		return nil
	}
	parks, err := d.findPersonParks(func(item ledger.WorkItem) bool {
		q := item.Park.Person
		return q.AskID == ask.ID && (q.HostID == "" || q.HostID == d.localHost().ID)
	})
	if err != nil {
		d.log.Printf("parks waiting on %s: %s", ask.ID, err)
	}
	reparked := 0
	for _, park := range parks {
		if err := d.reparkForThePerson(park); err != nil {
			d.log.Printf("re-park %s after its ask was dismissed: %s", park.item.ID, err)
			continue
		}
		if reparked++; reparked == 1 {
			d.tellParkDismissed(park)
		}
	}
	if reparked == 0 {
		d.tellAskDismissed(ask)
	}
	return nil
}

type remotePark struct {
	owner, key string
	item       ledger.WorkItem
}

func (d *Daemon) remotePersonParks(askID string) ([]remotePark, error) {
	store := d.ledgerStore()
	keys, err := store.ProjectKeys()
	if err != nil {
		return nil, err
	}
	here := d.localHost().ID
	var found []remotePark
	for _, key := range keys {
		owner, err := store.RemoteOwner(key)
		if err != nil {
			return nil, err
		}
		if owner == "" {
			continue
		}
		board, err := store.Board(key)
		if err != nil {
			return nil, err
		}
		for _, entry := range board.Backlog(ledger.BacklogQuery{Kinds: []ledger.ParkKind{ledger.ParkPerson}}, time.Time{}, ledger.Readings{}) {
			if q := entry.Item.Park.Person; q.AskID == askID && q.HostID == here {
				found = append(found, remotePark{owner: owner, key: key, item: entry.Item})
			}
		}
	}
	return found, nil
}

// The owner reports whether it reached the parker, whose id may mean nothing on the owner.
func (d *Daemon) forwardParkDismissal(ctx context.Context, p principal, park remotePark) (bool, error) {
	host, found := d.pairedHost(park.owner)
	if !found {
		return false, actionFailed(http.StatusBadGateway, "bad_gateway", fmt.Sprintf("%s is parked in a ledger kept by host %s, which is not paired here", park.item.ID, park.owner))
	}
	if err := d.hostTransit(ctx, host); err != nil {
		return false, err
	}
	if err := incompatibleHost(host); err != nil {
		return false, err
	}
	request := map[string]any{"action": dismissDecisionAction, "workItem": park.item.ID, "host": "", "sessionId": park.item.Park.Session}
	body, err := json.Marshal(request)
	if err != nil {
		return false, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	reply := d.forwardHostAction(ctx, p, "", host, request, body)
	if reply.Status == http.StatusNotFound {
		return true, nil
	}
	if reply.Status != http.StatusOK {
		return false, actionFailed(http.StatusBadGateway, "bad_gateway", fmt.Sprintf("host %s did not dismiss %s: %s", host.record.Name, park.item.ID, strings.TrimSpace(string(reply.Body))))
	}
	var answer struct {
		Result struct {
			ParkerTold bool `json:"parkerTold"`
		} `json:"result"`
	}
	_ = json.Unmarshal(reply.Body, &answer)
	return answer.Result.ParkerTold, nil
}

func (d *Daemon) tellAskDismissed(ask registry.Ask) {
	d.tellDismissed(ask.SessionID, fmt.Sprintf("[agenthail] The person dismissed your ask %q without answering.", askTitle(ask)), "decision-dismissed:"+ask.ID)
}

func offersDeliveryDismiss(ask registry.Ask) bool {
	return len(ask.Questions) > 0 && slices.ContainsFunc(ask.Questions[0].Options, func(option registry.AskOption) bool { return option.ID == registry.DeliveryDismiss })
}

func askTitle(ask registry.Ask) string {
	if title := strings.TrimSpace(ask.Title); title != "" || len(ask.Questions) == 0 {
		return title
	}
	return ask.Questions[0].Question
}

func (d *Daemon) withdrawAskForThePerson(ask registry.Ask) error {
	withdrawn, err := d.Registry.DismissAsk(ask.ID, time.Now())
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !withdrawn {
		return staleRequest()
	}
	if ask.RoutedTo != registry.AskToPerson {
		if _, err := d.Registry.CancelPendingByKey(askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
			d.log.Printf("withdraw the notice of %s: %s", ask.ID, err)
		}
	}
	d.sendWithdraws()
	d.publishAsk(ask.SessionID)
	return nil
}

func (d *Daemon) denyForThePerson(ctx context.Context, session *surface.Session, ask registry.Ask, actor string) error {
	adapter, err := d.adapters().For(session)
	if err != nil {
		return conflict(err.Error())
	}
	if err := d.ensureDashboardWritable(ctx, adapter, session); err != nil {
		if moved := sessionMovedError(err); moved != nil {
			return moved
		}
		return conflict(err.Error())
	}
	responder, err := permissionResponder(adapter, session, ask)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	state, err := responder.Requests(ctx, session)
	if err != nil {
		return actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
	}
	if _, open := openRequest(state, ask.Ref); !open {
		d.observeAfterAnswer(adapter, session)
		return staleRequest()
	}
	_, err = d.applyPermission(ctx, adapter, responder, session, ask, surface.Decision{Behavior: "deny", Message: dismissedDenial}, registry.AskToPerson, actor)
	return err
}

type personParked struct {
	key  string
	item ledger.WorkItem
}

func (d *Daemon) findPersonParks(match func(ledger.WorkItem) bool) ([]personParked, error) {
	store := d.ledgerStore()
	keys, err := store.ProjectKeys()
	if err != nil {
		return nil, err
	}
	var found []personParked
	for _, key := range keys {
		if owner, err := store.RemoteOwner(key); err != nil || owner != "" {
			continue
		}
		board, err := store.Board(key)
		if err != nil {
			continue
		}
		for _, entry := range board.Backlog(ledger.BacklogQuery{Kinds: []ledger.ParkKind{ledger.ParkPerson}}, time.Time{}, ledger.Readings{}) {
			if match(entry.Item) {
				found = append(found, personParked{key: key, item: entry.Item})
			}
		}
	}
	return found, nil
}

func (d *Daemon) dismissParked(itemID string) (bool, error) {
	parks, err := d.findPersonParks(func(item ledger.WorkItem) bool { return item.ID == itemID })
	if err != nil {
		return false, actionFailed(http.StatusInternalServerError, "ledger_error", err.Error())
	}
	if len(parks) == 0 {
		return false, actionFailed(http.StatusNotFound, "not_found", itemID+" is not parked on the person here")
	}
	if err := d.reparkForThePerson(parks[0]); err != nil {
		return false, err
	}
	d.withdrawParkAsk(*parks[0].item.Park.Person)
	return d.tellParkDismissed(parks[0]), nil
}

func (d *Daemon) reparkForThePerson(found personParked) error {
	parker := found.item.Park.Session
	if parker == "" {
		parker = surface.PersonSessionID
	}
	park := ledger.Park{Kind: ledger.ParkLater, Why: dismissedParkWhy}
	if _, err := d.ledgerStore().Work(ledger.WorkRequest{ProjectKey: found.key, SessionID: parker, Action: ledger.WorkPark, ItemID: found.item.ID, Park: &park}, time.Now()); err != nil {
		var refusal *ledger.Refusal
		if errors.As(err, &refusal) {
			return conflict(refusal.Reason)
		}
		return actionFailed(http.StatusInternalServerError, "ledger_error", err.Error())
	}
	d.ledgerChanged(found.key)
	return nil
}

func (d *Daemon) tellParkDismissed(found personParked) bool {
	item := found.item
	if item.Park.Session == "" {
		return false
	}
	return d.tellDismissed(item.Park.Session, fmt.Sprintf("[agenthail] The person dismissed %s (%s) without answering. It is parked as later.", item.ID, item.Park.Person.Question), parkDismissKey(found.key, item))
}

func (d *Daemon) withdrawParkAsk(question ledger.ParkQuestion) {
	if question.AskID == "" || question.HostID != "" && question.HostID != d.localHost().ID {
		return
	}
	ask, found, err := d.Registry.Ask(question.AskID)
	if err != nil || !found || ask.State != registry.AskOpen {
		return
	}
	if err := d.withdrawAskForThePerson(ask); err != nil {
		d.log.Printf("withdraw %s of a dismissed park: %s", ask.ID, err)
	}
}

func parkDismissKey(key string, item ledger.WorkItem) string {
	parkedAt := int64(0)
	if timeline := item.Timeline; len(timeline) > 0 {
		parkedAt = timeline[len(timeline)-1].At.UnixNano()
	}
	return fmt.Sprintf("decision-dismissed:%s:%s:%d", key, item.ID, parkedAt)
}

func (d *Daemon) tellDismissed(sessionID, message, key string) bool {
	if _, err := d.sendNotice(noticeRecipient{sessionID: sessionID, role: "asker"}, message, key); err != nil {
		d.log.Printf("tell %s the person dismissed its decision: %s", d.resolveDisplay(sessionID), err)
		return false
	}
	return true
}
