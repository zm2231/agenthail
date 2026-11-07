package registry

import (
	"database/sql"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// queryColumns names every table column a query reads, from SQLite's own
// program for it, so the list cannot fall behind the query. A column counts
// as read when the program loads it, seeks an index or rowid by it, or opens
// a partial index whose predicate names it.
func queryColumns(t *testing.T, db *sql.DB, query string) map[string][]string {
	t.Helper()
	type object struct{ name, table, kind, sql string }
	objects := map[int64]object{}
	schema, err := db.Query(`SELECT name,tbl_name,type,rootpage,COALESCE(sql,'') FROM sqlite_schema WHERE type IN ('table','index') AND rootpage>0`)
	if err != nil {
		t.Fatal(err)
	}
	for schema.Next() {
		var o object
		var root int64
		if err := schema.Scan(&o.name, &o.table, &o.kind, &root, &o.sql); err != nil {
			t.Fatal(err)
		}
		objects[root] = o
	}
	schema.Close()
	type column struct {
		at         int64
		name, kind string
		pk, key    bool
	}
	columns := func(o object) []column {
		pragma := `SELECT cid,name,type,pk>0,1 FROM pragma_table_info(?)`
		if o.kind == "index" {
			pragma = `SELECT seqno,COALESCE(name,''),'',0,key FROM pragma_index_xinfo(?)`
		}
		rows, err := db.Query(pragma, o.name)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []column
		for rows.Next() {
			var c column
			if err := rows.Scan(&c.at, &c.name, &c.kind, &c.pk, &c.key); err != nil {
				t.Fatal(err)
			}
			out = append(out, c)
		}
		return out
	}
	type step struct {
		opcode string
		p1, p2 int64
		p4     string
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
		steps = append(steps, step{opcode, p1, p2, p4.String})
	}
	program.Close()
	read := map[string][]string{}
	mark := func(table, name string) {
		if name != "" && !slices.Contains(read[table], name) {
			read[table] = append(read[table], name)
		}
	}
	identifier := regexp.MustCompile(`\w+`)
	cursors := map[int64]object{}
	for _, s := range steps {
		o, open := cursors[s.p1]
		switch s.opcode {
		case "OpenRead":
			if o, ok := objects[s.p2]; ok {
				cursors[s.p1] = o
				if where := strings.Index(strings.ToUpper(o.sql), " WHERE "); o.kind == "index" && where >= 0 {
					words := identifier.FindAllString(o.sql[where:], -1)
					for _, c := range columns(object{name: o.table, kind: "table"}) {
						if slices.Contains(words, c.name) {
							mark(o.table, c.name)
						}
					}
				}
			}
		case "Column":
			if !open {
				continue
			}
			for _, c := range columns(o) {
				if c.at == s.p2 {
					mark(o.table, c.name)
				}
			}
		case "SeekGE", "SeekGT", "SeekLE", "SeekLT", "IdxGE", "IdxGT", "IdxLE", "IdxLT", "Found", "NotFound", "NoConflict":
			if !open || o.kind != "index" {
				continue
			}
			fields, err := strconv.Atoi(s.p4)
			if err != nil {
				t.Fatalf("%s on %s has no key count: %q", s.opcode, o.name, s.p4)
			}
			for _, c := range columns(o) {
				if c.key && c.at < int64(fields) {
					mark(o.table, c.name)
				}
			}
		case "SeekRowid", "NotExists":
			if !open {
				continue
			}
			for _, c := range columns(o) {
				if c.pk && strings.EqualFold(c.kind, "INTEGER") {
					mark(o.table, c.name)
				}
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
	if !slices.Contains(read["sessions"], "status") || !slices.Contains(read["catalog_sessions"], "retired") || !slices.Contains(read["projects"], "id") || !slices.Contains(read["session_archive"], "session_id") {
		t.Fatalf("the query's columns were not read from its program: %v", read)
	}
	watched := versionTriggers(t, r.db.raw, "catalog_retired_version")
	// A sessions row reaches the counts only through the catalog_sessions row
	// that references it, whose own insert and delete are watched.
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
			if !has("UPDATE", column) {
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
