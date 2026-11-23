package daemon

import (
	"path/filepath"
	"regexp"
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

var (
	generatedSessionName = regexp.MustCompile(`^([a-z0-9]+(?:-[a-z0-9]+)*)-[0-9a-z]{2}$`)
	folderSlugBreaks     = regexp.MustCompile(`[^a-z0-9]+`)
)

// A push passes through the relay and Apple, so it names the session by its alias or by the name Claude Code generates from its folder (news-fqs-pilot-b9 in news-fqs-pilot), never by a name that may hold the person's own words: a Codex name can be the first line of a prompt.
func pushTag(kind surface.SurfaceKind, alias, name, cwd string) string {
	if alias != "" {
		return "@" + alias
	}
	if kind == surface.KindClaude && folderGeneratedName(name, cwd) {
		return name
	}
	switch kind {
	case surface.KindClaude:
		return "Claude agent"
	case surface.KindCodex:
		return "Codex agent"
	case surface.KindNotion:
		return "Notion agent"
	default:
		return "Agent"
	}
}

func folderGeneratedName(name, cwd string) bool {
	match := generatedSessionName.FindStringSubmatch(name)
	if match == nil || cwd == "" {
		return false
	}
	folder := strings.Trim(folderSlugBreaks.ReplaceAllString(strings.ToLower(filepath.Base(cwd)), "-"), "-")
	return folder != "" && strings.HasPrefix(folder, match[1])
}

func (d *Daemon) sessionPushTag(session *surface.Session) string {
	alias, err := d.Registry.ReverseAlias(session.ID)
	if err != nil {
		d.log.Printf("read the alias of %s: %s", session.ID, err)
	}
	return pushTag(session.Surface, alias, session.Name, session.Cwd)
}

type dashboardAsk struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	RoutedTo   string    `json:"routedTo"`
	AskedAt    time.Time `json:"askedAt"`
	AnsweredAt time.Time `json:"answeredAt,omitzero"`
	Deadline   time.Time `json:"deadline,omitzero"`
	Open       []string  `json:"open,omitempty"`
}

func askRow(ask *registry.Ask) *dashboardAsk {
	if ask == nil {
		return nil
	}
	return &dashboardAsk{ID: ask.ID, Title: ask.Title, RoutedTo: ask.RoutedTo, AskedAt: ask.CreatedAt, AnsweredAt: ask.AnsweredAt, Deadline: ask.Deadline, Open: ask.Open}
}

func (ask *dashboardAsk) asksPerson() bool {
	return ask != nil && ask.RoutedTo == registry.AskToPerson && ask.AnsweredAt.IsZero()
}

type turnCompletedNotice struct {
	title, desktop, mobile, requestID, outcome string
	actions                                    []surface.RequestAction
	quiet                                      bool
	agentStarted                               bool
}

// A turn that leaves an ask open for the person needs them; the ask was announced when it reached them, so the completion stays quiet.
func (d *Daemon) endTurnNotifications(session *surface.Session, completedTurnID, title, body string) turnCompletedNotice {
	notice := turnCompletedNotice{title: title, desktop: body, mobile: body, outcome: turnOutcomeFinished}
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
		notice.desktop = "Done: " + shownLine(end.Headline, headlineShownRunes)
		notice.mobile = notice.desktop
	}
	return notice
}
