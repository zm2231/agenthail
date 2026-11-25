package ledger

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

type WorkType string

const (
	WorkTask    WorkType = "task"
	WorkBug     WorkType = "bug"
	WorkFeature WorkType = "feature"
	WorkIdea    WorkType = "idea"
)

var workTypes = []WorkType{WorkTask, WorkBug, WorkFeature, WorkIdea}

var workPriorities = []string{"P0", "P1", "P2", "P3"}

const defaultWorkPriority = "P2"

const workCloseDone = "done"

var workCloseReasons = []string{"duplicate", "wontfix", "obsolete"}

const maxWorkFieldRunes = 8000

const maxWorkTitleRunes = 80

// WorkFields describe a work item. From names the item or clause it was found from.
type WorkFields struct {
	Type        WorkType `json:"type,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Description string   `json:"description,omitempty"`
	Acceptance  string   `json:"acceptance,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	From        string   `json:"from,omitempty"`
	Parent      string   `json:"parent,omitempty"`
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
	f.Description, f.Acceptance, f.From, f.Parent = strings.TrimSpace(f.Description), strings.TrimSpace(f.Acceptance), strings.TrimSpace(f.From), strings.TrimSpace(f.Parent)
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
	return "", refuse("unknown work type %q; use task, bug, feature or idea", s)
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

func workTitle(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", refuse("a work item needs its text")
	}
	if n := utf8.RuneCountInString(text); n > maxWorkTitleRunes {
		return "", refuse("a work item's title is at most %d characters (this one is %d); keep it to what the work is and put the rest in the description", maxWorkTitleRunes, n)
	}
	return text, nil
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
	// Parent moves the item under another; an empty one takes it out from under its parent.
	Parent *string `json:"parent,omitempty"`
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
	// FromClauseID is the clause a move takes the item off; empty when
	// found work is linked.
	FromClauseID string `json:"fromClauseId,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

func (b *Board) planWorkEdit(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	c := req.Change
	var changed []string
	if c.Text != nil {
		text, err := workTitle(*c.Text)
		if err != nil {
			return "", nil, "", err
		}
		c.Text, changed = &text, append(changed, "text")
	}
	if c.Type != nil {
		t, err := workType(string(*c.Type))
		if err != nil || t == "" {
			return "", nil, "", refuse("unknown work type %q; use task, bug or feature", *c.Type)
		}
		if t == WorkIdea {
			return "", nil, "", refuse("an idea keeps the person's words: agenthail ledger attach <utterance> --as idea --clause \"<title>\"")
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
	if c.Parent != nil {
		parent := strings.TrimSpace(*c.Parent)
		switch {
		case parent == "" && item.Parent == "":
			return "", nil, "", refuse("%s has no parent", item.ID)
		case parent == "":
			changed = append(changed, "no parent")
		default:
			p, err := b.checkParent(item.ID, parent, req.ProjectKey)
			if err != nil {
				return "", nil, "", err
			}
			if p.ID == item.Parent {
				return "", nil, "", refuse("%s is already a child of %s", item.ID, p.ID)
			}
			parent, changed = p.ID, append(changed, "parent "+p.ID)
		}
		c.Parent = &parent
	}
	if len(changed) == 0 {
		return "", nil, "", refuse("edit needs at least one of --text, --type, --priority, --description, --acceptance, --label, --unlabel, --parent or --no-parent")
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
	if p.Parent != nil && (*p.Parent == "" || b.workItems[*p.Parent] != nil) {
		b.releaseChild(item.Parent, item.ID)
		item.Parent = *p.Parent
		b.adoptChild(item.Parent, item.ID)
		if item.Parent == "" {
			changed = append(changed, "no parent")
		} else {
			changed = append(changed, "parent "+item.Parent)
		}
	}
	item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "edit", Text: strings.Join(changed, "; ")})
	return nil
}

// planWorkLink adopts found work into the person's intent by hanging it off a
// live clause, or moves linked work to another clause for a stated reason.
func (b *Board) planWorkLink(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	reason := strings.TrimSpace(req.Reason)
	switch {
	case item == nil:
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	case req.ClauseID == "":
		return "", nil, "", refuse("link needs the clause the work belongs to")
	case item.ClauseID == req.ClauseID:
		return "", nil, "", refuse("%s already hangs off %s", item.ID, item.ClauseID)
	case item.ClauseID != "" && reason == "":
		return "", nil, "", refuse("%s hangs off %s; moving it needs --reason: why it belongs to %s", item.ID, item.ClauseID, req.ClauseID)
	}
	if err := b.liveClause(req.ClauseID, req.ProjectKey); err != nil {
		return "", nil, "", err
	}
	payload := WorkLinkPayload{ItemID: item.ID, ClauseID: req.ClauseID, Actor: req.SessionID, FromClauseID: item.ClauseID}
	if item.ClauseID == "" {
		return EventWorkLink, payload, fmt.Sprintf("%s now hangs off %s", item.ID, req.ClauseID), nil
	}
	payload.Reason = reason
	readback := fmt.Sprintf("%s moved from %s to %s", item.ID, item.ClauseID, req.ClauseID)
	if from := b.clauses[item.ClauseID]; from != nil && !slices.ContainsFunc(from.WorkItems, func(other *WorkItem) bool {
		return other != item && other.State != WorkClosed && other.State != WorkDeleted
	}) {
		readback += fmt.Sprintf("; %s now has no work", from.ID)
	}
	return EventWorkLink, payload, readback, nil
}

func (b *Board) applyWorkLink(event Event) error {
	var p WorkLinkPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item, c := b.workItems[p.ItemID], b.clauses[p.ClauseID]
	if item == nil || item.ClauseID == p.ClauseID || item.ClauseID != p.FromClauseID && (item.ClauseID != "" || b.clauses[p.FromClauseID] != nil) {
		return nil
	}
	from := b.clauses[item.ClauseID]
	if from != nil {
		from.WorkItems = slices.DeleteFunc(from.WorkItems, func(have *WorkItem) bool { return have == item })
	}
	if c == nil {
		// The target's words were retracted on replay: the move still takes
		// the item off its old clause, so it is found work again.
		if item.ClauseID != "" {
			item.ClauseID = ""
			item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "link", Text: fmt.Sprintf("moved off %s: %s", p.FromClauseID, p.Reason)})
		}
		return nil
	}
	text := "hangs off " + c.ID
	if p.FromClauseID != "" {
		text = fmt.Sprintf("moved from %s to %s: %s", p.FromClauseID, c.ID, p.Reason)
	}
	item.ClauseID = c.ID
	c.WorkItems = append(c.WorkItems, item)
	item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "link", Text: text})
	return nil
}

func (a WorkAction) NestedKey(key string) string {
	var nested any
	var parent string
	switch a {
	case WorkAdd:
		nested, parent = WorkFields{}, "fields"
	case WorkEdit:
		nested, parent = WorkChange{}, "change"
	default:
		return ""
	}
	if !jsonKey(reflect.TypeOf(WorkRequest{}), key) && jsonKey(reflect.TypeOf(nested), key) {
		return parent + "." + key
	}
	return ""
}

func jsonKey(typ reflect.Type, key string) bool {
	for i := range typ.NumField() {
		if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name == key {
			return true
		}
	}
	return false
}
