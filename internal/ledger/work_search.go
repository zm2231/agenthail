package ledger

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 100
	snippetRunes       = 120
)

// WorkSearch finds work items across ledgers with the words ledger search
// matches. Without States it keeps every item but deleted ones; without
// Projects it reads every ledger this store keeps.
type WorkSearch struct {
	Text     string
	States   []WorkState
	Projects []string
	Limit    int
}

type WorkHit struct {
	ProjectKey string    `json:"projectKey"`
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	State      WorkState `json:"state"`
	Type       WorkType  `json:"type"`
	Priority   string    `json:"priority"`
	Assignee   string    `json:"assignee,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Matched    string    `json:"matched"`
	Snippet    string    `json:"snippet,omitempty"`
	rank       int
}

type WorkSearchResult struct {
	Items     []WorkHit `json:"items"`
	Truncated bool      `json:"truncated"`
}

const (
	rankExactID = iota
	rankTitle
	rankOther
)

func (s *Store) SearchWork(req WorkSearch) (WorkSearchResult, error) {
	limit := req.Limit
	switch {
	case limit == 0:
		limit = DefaultSearchLimit
	case limit < 0 || limit > MaxSearchLimit:
		return WorkSearchResult{}, refuse("limit is a number of items from 1 to %d", MaxSearchLimit)
	}
	terms := strings.Fields(req.Text)
	if len(terms) == 0 {
		return WorkSearchResult{}, refuse("q is required")
	}
	q, err := WorkQuery{Terms: terms, States: req.States, All: true}.normalized()
	if err != nil {
		return WorkSearchResult{}, err
	}
	keys := req.Projects
	if len(keys) == 0 {
		if keys, err = s.ProjectKeys(); err != nil {
			return WorkSearchResult{}, err
		}
	}
	hits := []WorkHit{}
	for _, key := range keys {
		board, err := s.Board(key)
		if err != nil {
			return WorkSearchResult{}, err
		}
		hits = append(hits, board.searchWork(key, q)...)
	}
	if len(terms) == 1 && utf8.RuneCountInString(terms[0]) < 2 {
		exact := hits[:0]
		for _, h := range hits {
			if h.rank == rankExactID {
				exact = append(exact, h)
			}
		}
		if len(exact) == 0 {
			return WorkSearchResult{}, refuse("search for at least 2 characters, or an exact item id")
		}
		hits = exact
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if as, bs := a.State.settled(), b.State.settled(); as != bs {
			return bs
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.ProjectKey != b.ProjectKey {
			return a.ProjectKey < b.ProjectKey
		}
		return idNumber(a.ID) < idNumber(b.ID)
	})
	result := WorkSearchResult{Items: hits}
	if len(hits) > limit {
		result.Items, result.Truncated = hits[:limit], true
	}
	return result, nil
}

func (b *Board) searchWork(key string, q WorkQuery) []WorkHit {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var hits []WorkHit
	for _, item := range b.workItems {
		if q.keeps(item) {
			hits = append(hits, item.hit(key, q.Terms))
		}
	}
	return hits
}

func (item *WorkItem) hit(key string, terms []string) WorkHit {
	h := WorkHit{ProjectKey: key, ID: item.ID, Title: item.Text, State: item.State, Type: item.Type, Priority: item.Priority,
		Assignee: item.Assignee, UpdatedAt: item.updated(), rank: rankOther}
	if len(terms) == 1 {
		if field := item.exactID(terms[0]); field != "" {
			h.rank, h.Matched = rankExactID, field
			return h
		}
	}
	title := strings.ToLower(item.Text)
	var missing []string
	for _, t := range terms {
		if !strings.Contains(title, t) {
			missing = append(missing, t)
		}
	}
	if len(missing) == 0 {
		h.rank, h.Matched = rankTitle, MatchTitle
		return h
	}
	for _, f := range item.searchFields() {
		lower := strings.ToLower(f.text)
		for _, t := range missing {
			if strings.Contains(lower, t) {
				h.Matched = f.name
				if f.name != MatchID && f.name != MatchTdID {
					h.Snippet = snippet(f.text, t)
				}
				return h
			}
		}
	}
	return h
}

func (item *WorkItem) exactID(term string) string {
	switch from := strings.ToLower(item.ImportedFrom); {
	case strings.ToLower(item.ID) == term:
		return MatchID
	case from != "" && (from == term || strings.HasSuffix(from, ":"+term)):
		return MatchTdID
	}
	return ""
}

func snippet(text, term string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	lower := []rune(strings.ToLower(string(runes)))
	if len(lower) != len(runes) {
		lower = runes
	}
	if len(runes) <= snippetRunes {
		return string(runes)
	}
	at := max(0, strings.Index(string(lower), term))
	at = utf8.RuneCountInString(string(lower)[:at])
	start := max(0, at-(snippetRunes-utf8.RuneCountInString(term))/2)
	end := min(len(runes), start+snippetRunes)
	start = max(0, end-snippetRunes)
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
