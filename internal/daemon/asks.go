package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	askPrefix          = "ask-"
	askSweepEvery      = 30 * time.Second
	defaultAskEscalate = 10 * time.Minute
)

type askOptionInput struct {
	Label string `json:"label"`
	Does  string `json:"does,omitempty"`
}

type askQuestionInput struct {
	Question    string             `json:"question"`
	Options     []askOptionInput   `json:"options"`
	Recommended *int               `json:"recommended,omitempty"`
	Links       []registry.AskLink `json:"links,omitempty"`
}

type askInput struct {
	Title     string             `json:"title"`
	Questions []askQuestionInput `json:"questions"`
}

func isAskID(id string) bool { return strings.HasPrefix(id, askPrefix) }

func validAsk(input askInput) error {
	tooLong := func(field, value string, limit int) error {
		if utf8.RuneCountInString(value) > limit {
			return fmt.Errorf("%s is at most %d characters", field, limit)
		}
		return nil
	}
	title := strings.TrimSpace(input.Title)
	if title == "" || strings.ContainsAny(title, "\r\n") {
		return errors.New("title is one line and required")
	}
	if err := tooLong("title", title, registry.MaxAskTitle); err != nil {
		return err
	}
	if len(input.Questions) == 0 || len(input.Questions) > 4 {
		return errors.New("an ask takes 1 to 4 questions")
	}
	asked := map[string]bool{}
	for i, question := range input.Questions {
		which := fmt.Sprintf("question %d", i+1)
		text := strings.TrimSpace(question.Question)
		if text == "" {
			return errors.New(which + " is empty")
		}
		if asked[text] {
			return errors.New(which + " repeats an earlier question; ask each question once")
		}
		asked[text] = true
		if err := tooLong(which, text, registry.MaxAskQuestion); err != nil {
			return err
		}
		if len(question.Options) == 0 || len(question.Options) > 6 {
			return errors.New(which + " takes 1 to 6 options")
		}
		for _, option := range question.Options {
			if strings.TrimSpace(option.Label) == "" {
				return errors.New(which + " has an option with no label")
			}
			if err := tooLong(which+" option label", option.Label, registry.MaxAskLabel); err != nil {
				return err
			}
			if err := tooLong(which+" option does", option.Does, registry.MaxAskDoes); err != nil {
				return err
			}
		}
		if question.Recommended != nil && (*question.Recommended < 0 || *question.Recommended >= len(question.Options)) {
			return fmt.Errorf("%s recommended is an option index from 0 to %d", which, len(question.Options)-1)
		}
		if len(question.Links) > 3 {
			return errors.New(which + " takes at most 3 links")
		}
		for _, link := range question.Links {
			parsed, err := url.Parse(link.URL)
			if strings.TrimSpace(link.Title) == "" || err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return errors.New(which + " has a link without a title or an http(s) url")
			}
		}
	}
	return nil
}

func askQuestions(input askInput) []registry.AskQuestion {
	questions := make([]registry.AskQuestion, 0, len(input.Questions))
	for _, in := range input.Questions {
		question := registry.AskQuestion{Question: strings.TrimSpace(in.Question), Links: in.Links}
		for i, option := range in.Options {
			question.Options = append(question.Options, registry.AskOption{ID: fmt.Sprintf("o%d", i), Label: strings.TrimSpace(option.Label), Does: strings.TrimSpace(option.Does)})
		}
		if in.Recommended != nil {
			question.Recommended = question.Options[*in.Recommended].ID
		}
		questions = append(questions, question)
	}
	return questions
}

func askRequest(ask registry.Ask) surface.Request {
	questions := make([]surface.Question, 0, len(ask.Questions))
	for _, in := range ask.Questions {
		question := surface.Question{Question: in.Question, Options: make([]surface.QuestionOption, 0, len(in.Options))}
		for i, option := range in.Options {
			question.Options = append(question.Options, surface.QuestionOption{ID: option.ID, Label: option.Label, Description: option.Does})
			if option.ID == in.Recommended {
				suggested := i
				question.Suggested = &suggested
			}
		}
		for _, link := range in.Links {
			question.Links = append(question.Links, surface.QuestionLink{Title: link.Title, URL: link.URL})
		}
		questions = append(questions, question)
	}
	return surface.Request{ID: ask.ID, Kind: surface.WaitingAsk, Title: ask.Title, Questions: questions, OptionsOnly: ask.Origin == registry.AskOriginDelivery, CreatedAt: ask.CreatedAt, Deadline: ask.Deadline}.WithOptionIDs()
}

// askHearer is who an ask goes to first: a session an agent or coordinator started reports to its lead or spawner while that one runs.
func (d *Daemon) askHearer(sessionID string) (noticeRecipient, bool) {
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return noticeRecipient{}, false
	}
	origin, found, err := d.Registry.SessionOrigin(session.ID)
	if err != nil || !found || origin.Via != registry.OriginAgent && origin.Via != registry.OriginCoordinator {
		return noticeRecipient{}, false
	}
	hearer, ok := d.noticeRecipient(*session)
	if !ok || hearer.sessionID == session.ID || !d.hears(hearer.sessionID) {
		return noticeRecipient{}, false
	}
	return hearer, true
}

func (d *Daemon) hears(sessionID string) bool {
	session, err := d.Registry.Session(sessionID)
	if err != nil || session.Ended {
		return false
	}
	archive, err := d.Registry.ArchiveState()
	if err != nil {
		return false
	}
	_, archived := archive.Sessions[sessionID]
	return !archived
}

type askInvalid struct{ error }

func (d *Daemon) raiseAsk(session surface.Session, input askInput) (registry.Ask, error) {
	if err := validAsk(input); err != nil {
		return registry.Ask{}, askInvalid{err}
	}
	ask := registry.Ask{SessionID: session.ID, Origin: registry.AskOriginAgent, Title: strings.TrimSpace(input.Title), Questions: askQuestions(input), RoutedTo: registry.AskToPerson}
	hearer, routed := d.askHearer(session.ID)
	if routed {
		ask.RoutedTo = hearer.sessionID
	}
	ask, err := d.Registry.RaiseAsk(ask, time.Now())
	if err != nil {
		return registry.Ask{}, err
	}
	if routed {
		ask = d.tellHearer(ask, hearer)
	} else {
		d.announceAsk(ask)
	}
	d.publishAsk(ask.SessionID)
	return ask, nil
}

func (d *Daemon) tellHearer(ask registry.Ask, hearer noticeRecipient) registry.Ask {
	if _, err := d.sendNotice(hearer, d.askNoticeFor(ask, hearer.sessionID), askNoticeKey(ask.ID, hearer.sessionID)); err != nil {
		d.log.Printf("tell %s that %s asks: %s", hearer.role, d.resolveDisplay(ask.SessionID), err)
		return d.escalateAsk(ask, registry.AskToPerson)
	}
	return ask
}

func askNoticeKey(askID, hearerID string) string {
	return "notice-" + askID + "-" + hearerID
}

func (d *Daemon) askNoticeFor(ask registry.Ask, hearerID string) string {
	header := fmt.Sprintf("%s asks: %s", d.resolveDisplay(ask.SessionID), ask.Title)
	if ask.Origin == registry.AskOriginDelivery {
		header = fmt.Sprintf("A message from %s: %s", d.resolveDisplay(ask.SessionID), ask.Title)
		if hearerID == ask.SessionID {
			header = "Your message: " + ask.Title
		}
	}
	return askNotice(header, ask, d.usesMCP(hearerID))
}

func askNotice(header string, ask registry.Ask, mcp bool) string {
	var b strings.Builder
	b.WriteString("[agenthail] " + header)
	for i, question := range ask.Questions {
		labels := make([]string, len(question.Options))
		for j, option := range question.Options {
			labels[j] = option.Label
			if option.Does != "" {
				labels[j] += " (" + option.Does + ")"
			}
			if option.ID == question.Recommended {
				labels[j] += " [recommended]"
			}
		}
		fmt.Fprintf(&b, "\n%d. %s %s", i+1, question.Question, strings.Join(labels, " | "))
	}
	switch {
	case ask.Origin == registry.AskOriginDelivery && mcp:
		fmt.Fprintf(&b, "\nAnswer it: agenthail_answer {\"requestId\":%q,\"decision\":\"<%s>\"}, or pass it on: agenthail_answer {\"requestId\":%q,\"decision\":\"escalate\"}", ask.ID, strings.ReplaceAll(deliveryOptionIDs(ask), ", ", "|"), ask.ID)
	case ask.Origin == registry.AskOriginDelivery:
		fmt.Fprintf(&b, "\nAnswer it: agenthail ask answer %s <%s>, or pass it on: agenthail ask escalate %s", ask.ID, strings.ReplaceAll(deliveryOptionIDs(ask), ", ", "|"), ask.ID)
	case mcp:
		fmt.Fprintf(&b, "\nAnswer it: agenthail_answer {\"requestId\":%q,\"answers\":{\"<question>\":\"<answer>\"}}, or pass it to the person: agenthail_answer {\"requestId\":%q,\"decision\":\"escalate\"}", ask.ID, ask.ID)
	default:
		fmt.Fprintf(&b, "\nAnswer it: agenthail ask answer %s \"<answer>\", or pass it to the person: agenthail ask escalate %s", ask.ID, ask.ID)
	}
	return b.String()
}

// escalateAsk moves an open ask one hop up: to its holder's own hearer while one runs, else to the person.
func (d *Daemon) escalateAsk(ask registry.Ask, to string) registry.Ask {
	hearer, routed := noticeRecipient{}, false
	if to == "" && ask.RoutedTo != registry.AskToPerson {
		hearer, routed = d.askHearer(ask.RoutedTo)
	}
	if routed {
		to = hearer.sessionID
	} else if to == "" {
		to = registry.AskToPerson
	}
	moved, err := d.Registry.RouteAsk(ask.ID, to, registry.AskEscalated, time.Now())
	if err != nil {
		d.log.Printf("escalate %s: %s", ask.ID, err)
		return ask
	}
	if !moved {
		return ask
	}
	if ask.RoutedTo != registry.AskToPerson {
		if _, err := d.Registry.CancelPendingByKey(askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
			d.log.Printf("withdraw the notice of %s: %s", ask.ID, err)
		}
	}
	current, _, err := d.Registry.Ask(ask.ID)
	if err != nil {
		d.log.Printf("read %s: %s", ask.ID, err)
		return ask
	}
	switch {
	case current.Origin == registry.AskOriginPermission:
		d.tellPermission(current)
	case routed:
		current = d.tellHearer(current, hearer)
	default:
		d.announceAsk(current)
	}
	d.publishAsk(current.SessionID)
	return current
}

func (d *Daemon) askEscalateAfter() time.Duration {
	config, err := LoadDashboardConfig()
	if err != nil || config.AskEscalateMinutes <= 0 {
		return defaultAskEscalate
	}
	return time.Duration(config.AskEscalateMinutes) * time.Minute
}

func (d *Daemon) runAskSweeps(ctx context.Context) {
	ticker := time.NewTicker(askSweepEvery)
	defer ticker.Stop()
	for {
		d.sweepAsks(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) sweepAsks(now time.Time) {
	d.sendWithdraws()
	open, err := d.Registry.OpenAsks()
	if err != nil {
		d.log.Printf("read open asks: %s", err)
		return
	}
	agentWindow, permissionWindow := d.askEscalateAfter(), d.permissionEscalateAfter()
	for _, ask := range open {
		if ask.RoutedTo == registry.AskToPerson {
			continue
		}
		window := agentWindow
		if ask.Origin == registry.AskOriginPermission {
			window = permissionWindow
		}
		if !d.hears(ask.RoutedTo) || now.Sub(ask.RoutedAt) >= window {
			d.escalateAsk(ask, "")
		}
	}
}

func askNotificationKey(askID string) string {
	sum := sha256.Sum256([]byte(askID))
	return "ask-" + hex.EncodeToString(sum[:16])
}

// Identical on the holding host and every viewer, so the relay delivers it to a phone once.
func askDedupeKey(hostID, askID string) string {
	if hostID == "" || askID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hostID + "\x00" + askID))
	return hex.EncodeToString(sum[:])
}

func (d *Daemon) announceAsk(ask registry.Ask) {
	session, err := d.Registry.Session(ask.SessionID)
	if err != nil {
		d.log.Printf("announce %s: %s", ask.ID, err)
		return
	}
	request := askRequest(ask)
	desktop := fmt.Sprintf("%s needs you: %s", d.resolveDisplay(session.ID), ask.Title)
	mobile := mobileNotification{Title: "Agenthail", Message: fmt.Sprintf("%s needs you%s: %s", notificationSurfaceName(session.Surface), d.onHost(), ask.Title), SessionID: session.ID, TurnID: ask.ID, EventType: "ask.opened",
		CollapseID: askNotificationKey(ask.ID), RequestID: ask.ID, DedupeKey: askDedupeKey(d.hostIdentity().ID, ask.ID)}
	d.background.start(func(ctx context.Context) {
		d.deliverAskNotifications(ctx, desktop, mobile, request.Actions())
		d.withdrawIfClosed(ask.ID)
	})
}

// An ask closed while its notification was on its way was withdrawn before
// the notification arrived, so it is withdrawn again.
func (d *Daemon) withdrawIfClosed(askID string) {
	current, found, err := d.Registry.Ask(askID)
	if err != nil {
		d.log.Printf("read %s: %s", askID, err)
		return
	}
	if !found || current.State != registry.AskOpen || current.RoutedTo != registry.AskToPerson {
		d.withdrawAsk(askID)
	}
}

// The desktop notification goes through a helper app that can take seconds
// to start, so the phone is told alongside it.
func (d *Daemon) deliverAskNotifications(ctx context.Context, desktop string, mobile mobileNotification, actions []surface.RequestAction) {
	mobile.Actions = actions
	var posted sync.WaitGroup
	posted.Go(func() {
		if err := d.notifyDesktop("Agenthail", desktop, mobile.SessionID, "agenthail-"+mobile.CollapseID, mobile.RequestID, actions); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
	})
	notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d.notifyPairedDevices(notificationCtx, mobile)
	posted.Wait()
}

func (d *Daemon) withdrawAsk(askID string) {
	if err := d.Registry.QueueNotificationWithdraw(askID); err != nil {
		d.log.Printf("record the withdraw of %s: %s", askID, err)
	}
	d.sendWithdraws()
}

func (d *Daemon) sendWithdraws() {
	pending, err := d.Registry.PendingNotificationWithdraws()
	if err != nil {
		d.log.Printf("read pending notification withdraws: %s", err)
		return
	}
	for _, w := range pending {
		d.withdrawMu.Lock()
		if d.withdrawing[w.AskID] {
			d.withdrawMu.Unlock()
			continue
		}
		if d.withdrawing == nil {
			d.withdrawing = map[string]bool{}
		}
		d.withdrawing[w.AskID] = true
		d.withdrawMu.Unlock()
		if !d.background.start(func(ctx context.Context) { d.withdrawNotification(ctx, w) }) {
			d.withdrawDone(w.AskID)
		}
	}
}

func (d *Daemon) withdrawDone(askID string) {
	d.withdrawMu.Lock()
	delete(d.withdrawing, askID)
	d.withdrawMu.Unlock()
}

func (d *Daemon) withdrawNotification(ctx context.Context, w registry.NotificationWithdraw) {
	key := askNotificationKey(w.AskID)
	if err := d.withdrawDesktop("agenthail-" + key); err != nil {
		d.log.Printf("withdraw desktop notification: %s", err)
	}
	withdrawCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	d.withdrawFromPairedDevices(withdrawCtx, key)
	cancel()
	if ctx.Err() != nil {
		d.withdrawDone(w.AskID)
		return
	}
	cleared, err := d.Registry.ClearNotificationWithdraw(w)
	d.withdrawDone(w.AskID)
	if err != nil {
		d.log.Printf("forget the withdraw of %s: %s", key, err)
		return
	}
	if !cleared {
		d.sendWithdraws()
	}
}

func (d *Daemon) publishAsk(sessionID string) {
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", sessionID, map[string]string{"source": "ask"})
}

// askAnswerer is who may answer an open ask: the person always, an agent only while the ask is routed to it.
func askAnswerer(ask registry.Ask, p principal) (string, error) {
	if p.Kind != principalSession || p.ForPerson {
		return registry.AskToPerson, nil
	}
	if ask.RoutedTo != p.Claimed {
		return "", actionFailed(http.StatusForbidden, "forbidden", "this ask is not routed to you")
	}
	return p.Claimed, nil
}

func composeAskAnswer(questions []surface.Question, decision surface.Decision) string {
	var lines []string
	for index, question := range questions {
		if answer := strings.TrimSpace(decision.Answers[question.Question]); answer != "" {
			lines = append(lines, fmt.Sprintf("%d. %s %s", index+1, question.Question, answer))
		}
	}
	if note := strings.TrimSpace(decision.Message); note != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, note)
	}
	return strings.Join(lines, "\n")
}

// answerAsk sends the answer to the asking session as one message, exactly
// once: the ask is claimed before delivery and released only when delivery
// provably did not happen.
func (d *Daemon) answerAsk(ctx context.Context, adapter surface.Surface, session *surface.Session, askID string, response surface.Response, p principal, personName string) (actionResult, error) {
	ask, found, err := d.Registry.Ask(askID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !found || ask.SessionID != session.ID || ask.State != registry.AskOpen {
		return actionResult{}, staleRequest()
	}
	by, err := askAnswerer(ask, p)
	if err != nil {
		return actionResult{}, err
	}
	if slices.Equal(response.Choices, []string{askEscalate}) {
		if by == registry.AskToPerson {
			return actionResult{}, invalidRequest("the person answers an ask; only an agent passes it on")
		}
		current := d.escalateAsk(ask, "")
		return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": askID, "outcome": "escalated", "routedTo": d.publicSessionID(current.RoutedTo)}}), nil
	}
	switch ask.Origin {
	case registry.AskOriginPermission:
		return d.answerPermission(ctx, adapter, session, ask, response, by, p.actor())
	case registry.AskOriginDelivery:
		return d.answerDelivery(ctx, ask, response, by, p.actor())
	case registry.AskOriginLedger:
		return d.answerLedger(ctx, ask, response, p)
	}
	request := askRequest(ask)
	decision, err := request.Resolve(response)
	if err != nil {
		return actionResult{}, invalidRequest(err.Error())
	}
	message := composeAskAnswer(request.Questions, decision)
	if message == "" {
		return actionResult{}, invalidRequest("answer at least one question or write a note")
	}
	claimed, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: decision.Answers, Note: strings.TrimSpace(decision.Message)}, by, p.actor(), time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return actionResult{}, staleRequest()
	}
	options := surface.SendOptions{Author: surface.AuthorPerson, PersonName: personName}
	if by != registry.AskToPerson {
		options = surface.SendOptions{SourceSessionID: by}
	}
	receipt, deliveryErr := coordinator.Sender{Service: d.coordinator()}.DeliverWithOptions(ctx, adapter, session, message, "", options)
	defer d.publishAsk(session.ID)
	if deliveryErr != nil {
		if surface.IsDeliveryTerminal(deliveryErr) || surface.IsDeliveryUnavailable(deliveryErr) {
			if err := d.Registry.ReleaseAsk(ask.ID); err != nil {
				d.log.Printf("release the answer to %s: %s", ask.ID, err)
			}
			return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", deliveryErr.Error())
		}
		d.settleAsk(ask, string(surface.EvidenceUnknown), "")
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", "the answer may have reached the session, so it is not sent again: "+deliveryErr.Error())
	}
	d.settleAsk(ask, receipt.Status, receipt.MessageID)
	entry := registry.HistoryEntry{Kind: "request-answered", SessionID: session.ID, Message: askID, Result: "answered by " + p.actor()}
	if err := d.Registry.RecordHistory(entry); err != nil {
		d.log.Printf("record the answer to %s: %s", askID, err)
	}
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": askID, "outcome": "answered", "receipt": receipt}}), nil
}

func (d *Daemon) settleAsk(ask registry.Ask, status, messageID string) {
	if err := d.Registry.RecordAskApplied(ask.ID, status, messageID, time.Now()); err != nil {
		d.log.Printf("record the answer to %s: %s", ask.ID, err)
	}
	if ask.RoutedTo != registry.AskToPerson {
		if _, err := d.Registry.CancelPendingByKey(askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
			d.log.Printf("withdraw the notice of %s: %s", ask.ID, err)
		}
	}
	d.sendWithdraws()
}

func (d *Daemon) withdrawOwnAsk(session surface.Session, askID string) error {
	ask, found, err := d.Registry.Ask(askID)
	if err != nil {
		return err
	}
	if !found || ask.SessionID != session.ID {
		return errAskNotFound
	}
	withdrawn, err := d.Registry.WithdrawAsk(askID, time.Now())
	if err != nil {
		return err
	}
	if !withdrawn {
		return errAskSettled
	}
	if ask.RoutedTo != registry.AskToPerson {
		if _, err := d.Registry.CancelPendingByKey(askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
			d.log.Printf("withdraw the notice of %s: %s", ask.ID, err)
		}
	}
	d.sendWithdraws()
	d.publishAsk(session.ID)
	return nil
}

const askEscalate = "escalate"

var (
	errAskNotFound = errors.New("no ask with that id from this session")
	errAskSettled  = errors.New("that ask is already answered or withdrawn")
)
