package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const AttentionAsk = "ask"

type dashboardEndTurn struct {
	Headline string    `json:"headline"`
	Next     []string  `json:"next,omitempty"`
	EndedAt  time.Time `json:"endedAt"`
}

const (
	headlineShownRunes = 80
	nextShownRunes     = 160
)

func endTurnRow(end *registry.TurnEnd) *dashboardEndTurn {
	if end == nil {
		return nil
	}
	var next []string
	for _, item := range end.Next {
		next = append(next, shownLine(item, nextShownRunes))
	}
	return &dashboardEndTurn{Headline: shownLine(end.Headline, headlineShownRunes), Next: next, EndedAt: end.EndedAt}
}

func shownLine(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit-1])
	if space := strings.LastIndexByte(cut, ' '); space > len(cut)/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,;:.-") + "…"
}

// A push passes through the relay and Apple, so it names the session by alias and never by its title, which is often the person's own words.
func pushSubject(kind surface.SurfaceKind, alias string, project *CatalogProject) string {
	who := notificationSurfaceName(kind)
	if alias != "" {
		who = "@" + alias
	}
	if project != nil && project.DisplayName != "" {
		who += " in " + project.DisplayName
	}
	return who
}

func (d *Daemon) sessionPushSubject(session *surface.Session) string {
	alias, err := d.Registry.ReverseAlias(session.ID)
	if err != nil {
		d.log.Printf("read the alias of %s: %s", session.ID, err)
	}
	project, err := d.sessionProject(*session)
	if err != nil {
		d.log.Printf("read the project of %s: %s", session.ID, err)
	}
	return pushSubject(session.Surface, alias, project)
}

type dashboardAsk struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	RoutedTo   string    `json:"routedTo"`
	AskedAt    time.Time `json:"askedAt"`
	AnsweredAt time.Time `json:"answeredAt,omitzero"`
	Deadline   time.Time `json:"deadline,omitzero"`
}

func askRow(ask *registry.Ask) *dashboardAsk {
	if ask == nil {
		return nil
	}
	return &dashboardAsk{ID: ask.ID, Title: ask.Title, RoutedTo: ask.RoutedTo, AskedAt: ask.CreatedAt, AnsweredAt: ask.AnsweredAt, Deadline: ask.Deadline}
}

func (ask *dashboardAsk) asksPerson() bool {
	return ask != nil && ask.RoutedTo == registry.AskToPerson && ask.AnsweredAt.IsZero()
}

type turnCompletedNotice struct {
	desktop, mobile, requestID, outcome string
	actions                             []surface.RequestAction
	quiet                               bool
}

// A turn that leaves an ask open for the person needs them; the ask was announced when it reached them, so the completion stays quiet.
func (d *Daemon) endTurnNotifications(session *surface.Session, completedTurnID, desktop, mobile string) turnCompletedNotice {
	notice := turnCompletedNotice{desktop: desktop, mobile: mobile, outcome: turnOutcomeFinished}
	ask, asked, err := d.Registry.RowAsk(session.ID)
	if err != nil {
		d.log.Printf("read the ask of %s: %s", d.resolveDisplay(session.ID), err)
	}
	if asked && ask.State == registry.AskOpen && ask.RoutedTo == registry.AskToPerson {
		notice.outcome, notice.requestID, notice.quiet = turnOutcomeNeedsYou, ask.ID, true
		return notice
	}
	end, found, err := d.Registry.CompletedTurnEnd(session.ID, completedTurnID)
	if err != nil {
		d.log.Printf("read turn end %s: %s", d.resolveDisplay(session.ID), err)
	}
	if found {
		headline := shownLine(end.Headline, headlineShownRunes)
		notice.desktop = fmt.Sprintf("%s done: %s", d.resolveDisplay(session.ID), headline)
		notice.mobile = fmt.Sprintf("%s done%s: %s", d.sessionPushSubject(session), d.onHost(), headline)
	}
	return notice
}
