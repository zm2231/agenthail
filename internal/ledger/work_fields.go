package ledger

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

type WorkType string

const (
	WorkTask    WorkType = "task"
	WorkBug     WorkType = "bug"
	WorkFeature WorkType = "feature"
)

var workTypes = []WorkType{WorkTask, WorkBug, WorkFeature}

var workPriorities = []string{"P0", "P1", "P2", "P3"}

const defaultWorkPriority = "P2"

const workCloseDone = "done"

var workCloseReasons = []string{"duplicate", "wontfix", "obsolete"}

const maxWorkFieldRunes = 8000

// WorkFields describe a work item. From names the item or clause it was found from.
type WorkFields struct {
	Type        WorkType `json:"type,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Description string   `json:"description,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	From        string   `json:"from,omitempty"`
}

func (f WorkFields) withDefaults() WorkFields {
	if f.Type == "" {
		f.Type = WorkTask
	}
	if f.Priority == "" {
		f.Priority = defaultWorkPriority
	}
	return f
}

func (f WorkFields) normalized() (WorkFields, error) {
	var err error
	if f.Type, err = workType(string(f.Type)); err != nil {
		return f, err
	}
	if f.Priority, err = workPriority(f.Priority); err != nil {
		return f, err
	}
	f.Description, f.Acceptance, f.From = strings.TrimSpace(f.Description), strings.TrimSpace(f.Acceptance), strings.TrimSpace(f.From)
	if err := fieldLength("description", f.Description); err != nil {
		return f, err
	}
	if err := fieldLength("acceptance", f.Acceptance); err != nil {
		return f, err
	}
	f.Labels = normalLabels(f.Labels)
	return f, nil
}

func workType(s string) (WorkType, error) {
	t := WorkType(strings.ToLower(strings.TrimSpace(s)))
	if t == "" || slices.Contains(workTypes, t) {
		return t, nil
	}
	return "", refuse("unknown work type %q; use task, bug or feature", s)
}

func workPriority(s string) (string, error) {
	p := strings.ToUpper(strings.TrimSpace(s))
	if p != "" && !strings.HasPrefix(p, "P") {
		p = "P" + p
	}
	if p == "" || slices.Contains(workPriorities, p) {
		return p, nil
	}
	return "", refuse("unknown priority %q; use P0 (most urgent) to P3", s)
}

func fieldLength(name, value string) error {
	if utf8.RuneCountInString(value) > maxWorkFieldRunes {
		return refuse("a work item's %s is at most %d characters; put the rest in a file and point to it", name, maxWorkFieldRunes)
	}
	return nil
}

func normalLabels(labels []string) []string {
	var out []string
	for _, l := range labels {
		for _, part := range strings.Split(l, ",") {
			if part = strings.ToLower(strings.TrimSpace(part)); part != "" && !slices.Contains(out, part) {
				out = append(out, part)
			}
		}
	}
	return out
}

func priorityRank(p string) int {
	if i := slices.Index(workPriorities, p); i >= 0 {
		return i
	}
	return slices.Index(workPriorities, defaultWorkPriority)
}

func closeReason(reason string) (word, rest string) {
	reason = strings.TrimSpace(reason)
	end := strings.IndexAny(reason, " :,")
	if end < 0 {
		return strings.ToLower(reason), ""
	}
	return strings.ToLower(reason[:end]), strings.TrimLeft(reason[end:], " :,")
}

func (item *WorkItem) origin() string {
	if item.ClauseID == "" {
		return "found"
	}
	return item.ClauseID
}

// WorkChange is an edit: nil leaves a field as it is, and labels are added or removed one by one.
type WorkChange struct {
	Text         *string   `json:"text,omitempty"`
	Type         *WorkType `json:"type,omitempty"`
	Priority     *string   `json:"priority,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Acceptance   *string   `json:"acceptance,omitempty"`
	AddLabels    []string  `json:"addLabels,omitempty"`
	RemoveLabels []string  `json:"removeLabels,omitempty"`
}

type WorkEditPayload struct {
	ItemID string `json:"itemId"`
	Actor  string `json:"actor"`
	WorkChange
}

type WorkLinkPayload struct {
	ItemID   string `json:"itemId"`
	ClauseID string `json:"clauseId"`
	Actor    string `json:"actor"`
}

func (b *Board) planWorkEdit(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	c := req.Change
	var changed []string
	if c.Text != nil {
		text := strings.TrimSpace(*c.Text)
		if text == "" {
			return "", nil, "", refuse("a work item needs its text")
		}
		c.Text, changed = &text, append(changed, "text")
	}
	if c.Type != nil {
		t, err := workType(string(*c.Type))
		if err != nil || t == "" {
			return "", nil, "", refuse("unknown work type %q; use task, bug or feature", *c.Type)
		}
		c.Type, changed = &t, append(changed, "type "+string(t))
	}
	if c.Priority != nil {
		p, err := workPriority(*c.Priority)
		if err != nil || p == "" {
			return "", nil, "", refuse("unknown priority %q; use P0 (most urgent) to P3", *c.Priority)
		}
		c.Priority, changed = &p, append(changed, "priority "+p)
	}
	for name, field := range map[string]**string{"description": &c.Description, "acceptance": &c.Acceptance} {
		if *field == nil {
			continue
		}
		v := strings.TrimSpace(**field)
		if err := fieldLength(name, v); err != nil {
			return "", nil, "", err
		}
		*field, changed = &v, append(changed, name)
	}
	c.AddLabels, c.RemoveLabels = normalLabels(c.AddLabels), normalLabels(c.RemoveLabels)
	for _, l := range c.AddLabels {
		changed = append(changed, "+"+l)
	}
	for _, l := range c.RemoveLabels {
		changed = append(changed, "-"+l)
	}
	if len(changed) == 0 {
		return "", nil, "", refuse("edit needs at least one of --text, --type, --priority, --description, --acceptance, --label or --unlabel")
	}
	slices.Sort(changed)
	return EventWorkEdit, WorkEditPayload{ItemID: item.ID, Actor: req.SessionID, WorkChange: c}, fmt.Sprintf("%s edited: %s", item.ID, strings.Join(changed, ", ")), nil
}

func (b *Board) applyWorkEdit(event Event) error {
	var p WorkEditPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item := b.workItems[p.ItemID]
	if item == nil {
		return nil
	}
	var changed []string
	if p.Text != nil {
		item.Text, changed = *p.Text, append(changed, "text: "+*p.Text)
	}
	if p.Type != nil {
		item.Type, changed = *p.Type, append(changed, "type "+string(*p.Type))
	}
	if p.Priority != nil {
		item.Priority, changed = *p.Priority, append(changed, "priority "+*p.Priority)
	}
	if p.Description != nil {
		item.Description, changed = *p.Description, append(changed, "description")
	}
	if p.Acceptance != nil {
		item.Acceptance, changed = *p.Acceptance, append(changed, "acceptance")
	}
	for _, l := range p.AddLabels {
		if !slices.Contains(item.Labels, l) {
			item.Labels = append(item.Labels, l)
		}
		changed = append(changed, "+"+l)
	}
	for _, l := range p.RemoveLabels {
		item.Labels = slices.DeleteFunc(item.Labels, func(have string) bool { return have == l })
		changed = append(changed, "-"+l)
	}
	item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "edit", Text: strings.Join(changed, "; ")})
	return nil
}

// planWorkLink adopts found work into the person's intent by hanging it off a live clause.
func (b *Board) planWorkLink(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	switch {
	case item == nil:
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	case item.ClauseID != "":
		return "", nil, "", refuse("%s already hangs off %s; only found work is linked", item.ID, item.ClauseID)
	case req.ClauseID == "":
		return "", nil, "", refuse("link needs the clause the work belongs to")
	}
	if err := b.liveClause(req.ClauseID, req.ProjectKey); err != nil {
		return "", nil, "", err
	}
	return EventWorkLink, WorkLinkPayload{ItemID: item.ID, ClauseID: req.ClauseID, Actor: req.SessionID}, fmt.Sprintf("%s now hangs off %s", item.ID, req.ClauseID), nil
}

func (b *Board) applyWorkLink(event Event) error {
	var p WorkLinkPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item, c := b.workItems[p.ItemID], b.clauses[p.ClauseID]
	if item == nil || c == nil || item.ClauseID != "" {
		return nil
	}
	item.ClauseID = c.ID
	c.WorkItems = append(c.WorkItems, item)
	item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "link", Text: "hangs off " + c.ID})
	return nil
}
