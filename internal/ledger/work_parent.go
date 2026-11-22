package ledger

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// waitsOn is what an item cannot finish before: its dependencies and its children.
func (b *Board) waitsOn(item *WorkItem) []string {
	return append(slices.Clone(item.DependsOn), item.Children...)
}

func (b *Board) openChildren(item *WorkItem) []*WorkItem {
	var open []*WorkItem
	for _, id := range item.Children {
		if child := b.workItems[id]; child != nil && !child.State.settled() {
			open = append(open, child)
		}
	}
	sort.Slice(open, func(i, j int) bool { return idNumber(open[i].ID) < idNumber(open[j].ID) })
	return open
}

func (b *Board) refuseOpenChildren(item *WorkItem, action WorkAction) error {
	open := b.openChildren(item)
	if len(open) == 0 {
		return nil
	}
	names := make([]string, len(open))
	for i, child := range open {
		names[i] = fmt.Sprintf("%s %s", child.ID, child.State)
	}
	noun := "children"
	if len(open) == 1 {
		noun = "child"
	}
	return refuse("%s has %d open %s: %s; finish, close or move them before %s", item.ID, len(open), noun, strings.Join(names, ", "), actionGerund(action))
}

func actionGerund(action WorkAction) string {
	switch action {
	case WorkSubmit:
		return "submitting it"
	case WorkApprove:
		return "approving it"
	case WorkDelete:
		return "deleting it"
	default:
		return "closing it"
	}
}

// refuseSettledParent keeps a finished item from gaining or regaining an open child.
func (b *Board) refuseSettledParent(child *WorkItem, action WorkAction) error {
	parent := b.workItems[child.Parent]
	if parent == nil || !parent.State.settled() {
		return nil
	}
	return refuse("%s is a child of %s, which is %s; reopen %s before you %s %s", child.ID, parent.ID, parent.State, parent.ID, action, child.ID)
}

// checkParent is why item (empty while it is being added) cannot sit under parent.
func (b *Board) checkParent(item, parent, projectKey string) (*WorkItem, error) {
	p := b.workItems[parent]
	switch {
	case p == nil:
		return nil, refuse("--parent %q is not a work item in %s", parent, projectKey)
	case p.ID == item:
		return nil, refuse("%s cannot be its own parent", item)
	case p.State.settled():
		return nil, refuse("%s is %s; reopen it before giving it children", p.ID, p.State)
	case item != "" && b.reaches(item, p.ID):
		return nil, refuse("%s already waits on %s, directly or through others; making it %s's child would be a cycle", item, p.ID, p.ID)
	}
	return p, nil
}

func (b *Board) adoptChild(parent, child string) {
	if p := b.workItems[parent]; p != nil && !slices.Contains(p.Children, child) {
		p.Children = append(p.Children, child)
	}
}

func (b *Board) releaseChild(parent, child string) {
	if p := b.workItems[parent]; p != nil {
		p.Children = slices.DeleteFunc(p.Children, func(id string) bool { return id == child })
	}
}

func (b *Board) planWorkUndep(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	switch {
	case item == nil:
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	case !slices.Contains(item.DependsOn, req.DependsOn):
		return "", nil, "", refuse("%s does not depend on %s", item.ID, req.DependsOn)
	}
	return EventWorkItemUndep, WorkDepPayload{ItemID: item.ID, DependsOn: req.DependsOn, Actor: req.SessionID}, fmt.Sprintf("%s no longer waits on %s", item.ID, req.DependsOn), nil
}

func (b *Board) applyWorkUndep(event Event) error {
	var p WorkDepPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	if item := b.workItems[p.ItemID]; item != nil && slices.Contains(item.DependsOn, p.DependsOn) {
		item.DependsOn = slices.DeleteFunc(item.DependsOn, func(id string) bool { return id == p.DependsOn })
		item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "dep", Text: "no longer waits on " + p.DependsOn})
	}
	return nil
}

// WorkTree is every item as a forest: the roots are items with no parent on the board.
type WorkTree struct {
	WorkRef
	Assignee string     `json:"assignee,omitempty"`
	Children []WorkTree `json:"children,omitempty"`
}

// Tree is the items under root, or every root when root is empty; settled items appear only with all.
func (b *Board) Tree(root string, all bool) ([]WorkTree, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	keep := func(item *WorkItem) bool { return item.State != WorkDeleted && (all || !item.State.settled()) }
	var build func(item *WorkItem) WorkTree
	build = func(item *WorkItem) WorkTree {
		node := WorkTree{WorkRef: WorkRef{ID: item.ID, State: item.State, Text: item.Text}, Assignee: item.Assignee}
		children := slices.Clone(item.Children)
		sort.Slice(children, func(i, j int) bool { return idNumber(children[i]) < idNumber(children[j]) })
		for _, id := range children {
			if child := b.workItems[id]; child != nil && keep(child) {
				node.Children = append(node.Children, build(child))
			}
		}
		return node
	}
	if root != "" {
		item := b.workItem(root)
		if item == nil {
			return nil, false
		}
		return []WorkTree{build(item)}, true
	}
	var roots []*WorkItem
	for _, item := range b.workItems {
		if parent := b.workItems[item.Parent]; keep(item) && (parent == nil || parent.State == WorkDeleted) {
			roots = append(roots, item)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return idNumber(roots[i].ID) < idNumber(roots[j].ID) })
	out := make([]WorkTree, 0, len(roots))
	for _, item := range roots {
		out = append(out, build(item))
	}
	return out, true
}
