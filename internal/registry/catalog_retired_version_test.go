package registry

import (
	"database/sql"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// queryColumns names every table column a query reads, from SQLite's own
// program for it, so the list cannot fall behind the query.
func queryColumns(t *testing.T, db *sql.DB, query string) map[string][]string {
	t.Helper()
	type object struct{ name, table, kind string }
	objects := map[int64]object{}
	schema, err := db.Query(`SELECT name,tbl_name,type,rootpage FROM sqlite_schema WHERE type IN ('table','index') AND rootpage>0`)
	if err != nil {
		t.Fatal(err)
	}
	for schema.Next() {
		var o object
		var root int64
		if err := schema.Scan(&o.name, &o.table, &o.kind, &root); err != nil {
			t.Fatal(err)
		}
		objects[root] = o
	}
	schema.Close()
	names := func(pragma, name string) map[int64]string {
		rows, err := db.Query(`SELECT `+map[string]string{"table_info": "cid,name", "index_xinfo": "seqno,COALESCE(name,'')"}[pragma]+` FROM pragma_`+pragma+`(?)`, name)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[int64]string{}
		for rows.Next() {
			var at int64
			var column string
			if err := rows.Scan(&at, &column); err != nil {
				t.Fatal(err)
			}
			out[at] = column
		}
		return out
	}
	type step struct {
		opcode string
		p1, p2 int64
	}
	var steps []step
	program, err := db.Query(`EXPLAIN ` + query)
	if err != nil {
		t.Fatal(err)
	}
	for program.Next() {
		var addr, p1, p2, p3, p5 int64
		var opcode string
		var p4, comment sql.NullString
		if err := program.Scan(&addr, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, step{opcode, p1, p2})
	}
	program.Close()
	cursors := map[int64]object{}
	read := map[string][]string{}
	for _, s := range steps {
		switch s.opcode {
		case "OpenRead":
			if o, ok := objects[s.p2]; ok {
				cursors[s.p1] = o
			}
		case "Column":
			o, ok := cursors[s.p1]
			if !ok {
				continue
			}
			pragma := "table_info"
			if o.kind == "index" {
				pragma = "index_xinfo"
			}
			if column := names(pragma, o.name)[s.p2]; column != "" && !slices.Contains(read[o.table], column) {
				read[o.table] = append(read[o.table], column)
			}
		}
	}
	return read
}

type versionTrigger struct {
	event   string
	columns []string
}

func versionTriggers(t *testing.T, db *sql.DB, version string) map[string][]versionTrigger {
	t.Helper()
	rows, err := db.Query(`SELECT tbl_name,sql FROM sqlite_schema WHERE type='trigger' AND sql LIKE '%UPDATE '||?||' SET version=version+1%'`, version)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	event := regexp.MustCompile(`(?is)AFTER\s+(INSERT|DELETE|UPDATE)(?:\s+OF\s+([\w,\s]+?))?\s+ON\s+`)
	out := map[string][]versionTrigger{}
	for rows.Next() {
		var table, body string
		if err := rows.Scan(&table, &body); err != nil {
			t.Fatal(err)
		}
		match := event.FindStringSubmatch(body)
		if match == nil {
			t.Fatalf("cannot read the event of a %s trigger: %s", table, body)
		}
		trigger := versionTrigger{event: strings.ToUpper(match[1])}
		for _, column := range strings.Split(match[2], ",") {
			if column = strings.TrimSpace(column); column != "" {
				trigger.columns = append(trigger.columns, column)
			}
		}
		out[table] = append(out[table], trigger)
	}
	return out
}

func TestEveryValueTheRetiredCountsReadMovesTheirVersion(t *testing.T) {
	r := openTestRegistry(t)
	read := queryColumns(t, r.db.raw, catalogRetiredCountsQuery)
	if !slices.Contains(read["sessions"], "status") || !slices.Contains(read["catalog_sessions"], "host_project") {
		t.Fatalf("the query's columns were not read from its program: %v", read)
	}
	watched := versionTriggers(t, r.db.raw, "catalog_retired_version")
	// A row's key is never rewritten in place, and a sessions row reaches the
	// counts only through its catalog_sessions row, whose own insert and
	// delete are watched.
	keys := map[string]string{"catalog_sessions": "session_id", "sessions": "id", "session_archive": "session_id", "projects": "id"}
	joinedOnly := map[string]bool{"sessions": true}
	for table, columns := range read {
		has := func(event, column string) bool {
			for _, trigger := range watched[table] {
				if trigger.event == event && (column == "" || len(trigger.columns) == 0 || slices.Contains(trigger.columns, column)) {
					return true
				}
			}
			return false
		}
		for _, column := range columns {
			if column != keys[table] && !has("UPDATE", column) {
				t.Errorf("the retired counts read %s.%s, but changing it leaves catalog_retired_version as it was", table, column)
			}
		}
		if joinedOnly[table] {
			continue
		}
		for _, event := range []string{"INSERT", "DELETE"} {
			if !has(event, "") {
				t.Errorf("the retired counts read %s, but an %s there leaves catalog_retired_version as it was", table, event)
			}
		}
	}
}
