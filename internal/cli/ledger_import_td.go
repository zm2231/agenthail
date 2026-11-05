package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

type tdIssue struct {
	id, title, description, status, typ, priority, labels, parent, acceptance, creator string
	created                                                                            time.Time
	deleted, extras                                                                    bool
}

type tdExport struct {
	items    []ledger.WorkImport
	deps     []ledger.WorkImportDep
	leftOver []string
}

func tdOrigin(id string) string { return "td:" + id }

func tdActor(session string) string {
	if session == "" {
		return "td"
	}
	return "td:" + session
}

func openTD(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open td database %s: %w", path, err)
	}
	return db, nil
}

func tdTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func readTD(db *sql.DB, all bool) (tdExport, error) {
	var out tdExport
	issues, err := tdIssues(db)
	if err != nil {
		return out, err
	}
	imported := map[string]bool{}
	var deleted, closed, reset, extras int
	for _, is := range issues {
		switch {
		case is.deleted:
			deleted++
			continue
		case is.status == "closed" && !all:
			closed++
			continue
		}
		imported[is.id] = true
		if is.extras {
			extras++
		}
		if is.status == "in_progress" || is.status == "in_review" {
			reset++
		}
	}
	timelines, handoffs, reviews, notes, err := tdHistory(db, imported)
	if err != nil {
		return out, err
	}
	for _, is := range issues {
		if !imported[is.id] {
			continue
		}
		item := ledger.WorkImport{ImportedFrom: tdOrigin(is.id), Text: is.title, CreatedAt: is.created, CreatedBy: tdActor(is.creator),
			Fields: ledger.WorkFields{Type: ledger.WorkType(is.typ), Priority: is.priority, Description: is.description, Acceptance: is.acceptance, Labels: strings.FieldsFunc(is.labels, func(r rune) bool { return r == ',' })},
			State:  ledger.WorkOpen, Timeline: timelines[is.id], Handoff: handoffs[is.id]}
		switch is.typ {
		case "epic":
			item.Fields.Type, item.Fields.Labels = ledger.WorkFeature, append(item.Fields.Labels, "epic")
		case "task", "bug", "feature":
		default:
			item.Fields.Type, item.Fields.Labels = ledger.WorkTask, append(item.Fields.Labels, is.typ)
		}
		if is.priority == "P4" {
			item.Fields.Priority = "P3"
		}
		switch is.status {
		case "closed":
			item.State, item.Reviewer = ledger.WorkDone, reviews[is.id]
		case "blocked":
			item.State, item.Reason = ledger.WorkBlocked, "blocked in td"
		}
		out.items = append(out.items, item)
		if is.parent != "" {
			out.deps = append(out.deps, ledger.WorkImportDep{Item: tdOrigin(is.parent), On: tdOrigin(is.id)})
		}
	}
	rows, err := db.Query(`SELECT issue_id, depends_on_id FROM issue_dependencies WHERE relation_type='depends_on' ORDER BY issue_id, depends_on_id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item, on string
		if err := rows.Scan(&item, &on); err != nil {
			return out, err
		}
		if imported[item] && imported[on] {
			out.deps = append(out.deps, ledger.WorkImportDep{Item: tdOrigin(item), On: tdOrigin(on)})
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	leftOver := []struct {
		n    int
		what string
	}{
		{deleted, "deleted issues"},
		{closed, "closed issues (--all brings them over as done)"},
		{reset, "in-progress or in-review issues, imported as open: their claims were td sessions, not ledger sessions"},
		{extras, "issues with points, sprint, minor, due or defer dates, which the ledger does not keep"},
	}
	for _, l := range leftOver {
		if l.n > 0 {
			out.leftOver = append(out.leftOver, fmt.Sprintf("%d %s", l.n, l.what))
		}
	}
	out.leftOver = append(out.leftOver, notes...)
	for _, t := range []struct{ table, what string }{
		{"notes", "notes"}, {"issue_files", "linked files"}, {"git_snapshots", "git snapshots"}, {"work_sessions", "work sessions"},
		{"boards", "saved boards beyond the built-in one"},
	} {
		var n int
		query := `SELECT COUNT(*) FROM ` + t.table
		if t.table == "boards" {
			query += ` WHERE is_builtin=0`
		}
		if db.QueryRow(query).Scan(&n) == nil && n > 0 {
			out.leftOver = append(out.leftOver, fmt.Sprintf("%d %s", n, t.what))
		}
	}
	return out, nil
}

func tdIssues(db *sql.DB) ([]tdIssue, error) {
	rows, err := db.Query(`SELECT id, title, COALESCE(description,''), status, type, priority, COALESCE(labels,''), COALESCE(parent_id,''),
		COALESCE(acceptance,''), COALESCE(creator_session,''), CAST(created_at AS TEXT), deleted_at IS NOT NULL,
		COALESCE(points,0)>0 OR COALESCE(sprint,'')!='' OR COALESCE(minor,0)!=0 OR due_date IS NOT NULL OR defer_until IS NOT NULL
		FROM issues ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var issues []tdIssue
	for rows.Next() {
		var is tdIssue
		var created string
		if err := rows.Scan(&is.id, &is.title, &is.description, &is.status, &is.typ, &is.priority, &is.labels, &is.parent, &is.acceptance, &is.creator, &created, &is.deleted, &is.extras); err != nil {
			return nil, err
		}
		is.created = tdTime(created)
		issues = append(issues, is)
	}
	return issues, rows.Err()
}

func tdHistory(db *sql.DB, imported map[string]bool) (map[string][]ledger.WorkEntry, map[string]*ledger.Handoff, map[string]string, []string, error) {
	timelines := map[string][]ledger.WorkEntry{}
	handoffs := map[string]*ledger.Handoff{}
	reviews := map[string]string{}
	retyped := map[string]int{}
	add := func(issue string, e ledger.WorkEntry) {
		if imported[issue] {
			timelines[issue] = append(timelines[issue], e)
		}
	}
	err := tdRows(db, `SELECT issue_id, session_id, text, CAST(created_at AS TEXT) FROM comments`, func(v []string) {
		add(v[0], ledger.WorkEntry{At: tdTime(v[3]), Actor: tdActor(v[1]), Kind: "comment", Text: v[2]})
	})
	if err == nil {
		err = tdRows(db, `SELECT issue_id, session_id, message, type, CAST(timestamp AS TEXT) FROM logs WHERE issue_id!=''`, func(v []string) {
			typ, text := v[3], v[2]
			if !slices.Contains(ledger.WorkLogTypes, typ) {
				if imported[v[0]] {
					retyped[typ]++
				}
				typ, text = "progress", typ+": "+text
			}
			add(v[0], ledger.WorkEntry{At: tdTime(v[4]), Actor: tdActor(v[1]), Kind: "log", LogType: typ, Text: text})
		})
	}
	if err == nil {
		err = tdRows(db, `SELECT issue_id, session_id, done, remaining, decisions, uncertain, CAST(timestamp AS TEXT) FROM handoffs ORDER BY timestamp`, func(v []string) {
			h := &ledger.Handoff{Done: tdList(v[2]), Remaining: tdList(v[3]), Decisions: tdList(v[4]), Uncertain: tdList(v[5]), By: tdActor(v[1])}
			if h.Done == nil && h.Remaining == nil && h.Decisions == nil && h.Uncertain == nil {
				return
			}
			handoffs[v[0]] = h
			add(v[0], ledger.WorkEntry{At: tdTime(v[6]), Actor: h.By, Kind: "handoff",
				Text: fmt.Sprintf("done %q, remaining %q, decisions %q, uncertain %q", h.Done, h.Remaining, h.Decisions, h.Uncertain)})
		})
	}
	if err == nil {
		err = tdRows(db, `SELECT issue_id, COALESCE(NULLIF(reviewed_by,''), reviewer_session), decision, COALESCE(summary,''), CAST(created_at AS TEXT) FROM issue_reviews
			WHERE superseded_at IS NULL ORDER BY created_at`, func(v []string) {
			if v[2] == "approved" {
				reviews[v[0]] = tdActor(v[1])
			}
			text := v[2]
			if v[3] != "" {
				text += ": " + v[3]
			}
			add(v[0], ledger.WorkEntry{At: tdTime(v[4]), Actor: tdActor(v[1]), Kind: "review", Text: text})
		})
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for id := range timelines {
		sort.SliceStable(timelines[id], func(i, j int) bool { return timelines[id][i].At.Before(timelines[id][j].At) })
	}
	var notes []string
	for typ, n := range retyped {
		notes = append(notes, fmt.Sprintf("%d %q log entries, kept as progress logs starting %q", n, typ, typ+":"))
	}
	sort.Strings(notes)
	return timelines, handoffs, reviews, notes, nil
}

func tdRows(db *sql.DB, query string, each func([]string)) error {
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		v := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range v {
			ptrs[i] = &v[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		s := make([]string, len(v))
		for i := range v {
			s[i] = v[i].String
		}
		each(s)
	}
	return rows.Err()
}

func tdList(raw string) []string {
	var list []string
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil
	}
	var out []string
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
