package registry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// An update moves the version only when a watched value changes, so a writer
// that rewrites a row on every poll keeps cached views. An input without
// columns watches every column its table has when the triggers are made.
type presentationInput struct {
	table   string
	columns string
	when    string
	shown   map[string]string
}

var presentationInputs = []presentationInput{
	{table: "aliases"},
	{table: "session_origins"},
	{table: "tasks", columns: "child_session_id"},
	{table: "attention_items"},
	{table: "limit_resumes"},
	{table: "delivery_intents", when: outgoingIntentRow},
	{table: "message_queue"},
	{table: "session_archive"},
	{table: "projects"},
	{table: "coordinators"},
	{table: "coordinator_history"},
	{table: "coordinator_replaced"},
	{table: "turn_ends"},
	{table: "asks"},
	{table: "work_records", columns: presentationWorkColumns, when: `%[1]s.session_id<>''`},
	{table: "session_prs", columns: presentationPRColumns, when: `%[1]s.unlinked=0`, shown: map[string]string{"read_failures": fmt.Sprintf("(%%[1]s.read_failures>=%d)", PRReadFailureLimit)}},
	{table: "catalog_retired_version"},
	{table: "session_moves", columns: "conversation,peer_host,state,detail,outcome_unknown_at", when: `%[1]s.role='out'`},
	{table: "session_runtime", columns: "session_id,completed_turn_id"},
	{table: "session_keep_alive", columns: "session_id,failed_at_ms,error"},
	{table: "sessions", columns: "id,name,surface,status,ended,cwd,last_active_ms,runtime_kind,source,transport,host_id,parent_session_id,root_session_id,subagent_depth,agent_nickname,agent_role,registered_at,pinned_at_ms,snoozed_at_ms,snoozed_until_ms,snooze_wake"},
	{table: "catalog_sessions", columns: "session_id,unavailable_reason,waiting_on,background_tasks,usage_limit,turn_stop,host_queue_count,projection_generation,projection_fingerprint,misses,discovery_failures,retired,current,host_project"},
}

// Must cover every row catalogPresentationOutgoing and settledDeliveryFailure read.
const outgoingIntentRow = `(%[1]s.status IN ('failed','held') OR (%[1]s.status IN ('sent','delivered') AND EXISTS (SELECT 1 FROM delivery_intents f
	WHERE f.sender_session_id=%[1]s.sender_session_id AND f.target_session_id=%[1]s.target_session_id AND f.status='failed' AND f.id<%[1]s.id)))`

func (input presentationInput) triggerWhen(event string, columns []string) string {
	if event == "update" {
		changed := make([]string, 0, len(columns))
		for _, column := range columns {
			value := "%[1]s." + column
			if expression, ok := input.shown[column]; ok {
				value = expression
			}
			changed = append(changed, fmt.Sprintf(value, "NEW")+" IS NOT "+fmt.Sprintf(value, "OLD"))
		}
		when := "(" + strings.Join(changed, " OR ") + ")"
		if input.when != "" {
			when = "(" + fmt.Sprintf(input.when, "NEW") + " OR " + fmt.Sprintf(input.when, "OLD") + ") AND " + when
		}
		return " WHEN " + when
	}
	switch {
	case input.when == "":
		return ""
	case event == "insert":
		return " WHEN " + fmt.Sprintf(input.when, "NEW")
	}
	return " WHEN " + fmt.Sprintf(input.when, "OLD")
}

// dropPresentationTriggers comes before any migration step that drops a
// column, since a trigger naming a column keeps it from being dropped.
func dropPresentationTriggers(tx *sql.Tx) error {
	names, err := tx.Query(`SELECT name FROM sqlite_schema WHERE type='trigger' AND name LIKE 'catalog_presentation_version_%'`)
	if err != nil {
		return err
	}
	var drop []string
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			names.Close()
			return err
		}
		drop = append(drop, name)
	}
	if err := names.Close(); err != nil {
		return err
	}
	for _, name := range drop {
		if _, err := tx.Exec(`DROP TRIGGER ` + name); err != nil {
			return err
		}
	}
	return nil
}

// createPresentationTriggers comes after every migration step that adds a
// column, so the triggers name the columns the tables end with.
func createPresentationTriggers(tx *sql.Tx) error {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO catalog_presentation_version(id,version) VALUES(1,0)`); err != nil {
		return err
	}
	for _, input := range presentationInputs {
		columns, err := input.watched(tx)
		if err != nil {
			return err
		}
		for _, event := range []string{"insert", "update", "delete"} {
			name := fmt.Sprintf("catalog_presentation_version_%s_%s", input.table, event)
			on := strings.ToUpper(event)
			if event == "update" {
				on += " OF " + strings.Join(columns, ",")
			}
			if _, err := tx.Exec(fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON %s%s\nBEGIN\n\tUPDATE catalog_presentation_version SET version=version+1;\nEND", name, on, input.table, input.triggerWhen(event, columns))); err != nil {
				return fmt.Errorf("presentation trigger %s: %w", name, err)
			}
		}
	}
	return nil
}

func (input presentationInput) watched(tx *sql.Tx) ([]string, error) {
	if input.columns != "" {
		return strings.Split(input.columns, ","), nil
	}
	rows, err := tx.Query(`SELECT name FROM pragma_table_xinfo(?) WHERE hidden=0 ORDER BY name`, input.table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("presentation input %s has no columns", input.table)
	}
	return columns, rows.Err()
}

func presentationVersionTx(ctx context.Context, tx *sql.Tx) (uint64, error) {
	var version uint64
	err := tx.QueryRowContext(ctx, `SELECT version FROM catalog_presentation_version WHERE id=1`).Scan(&version)
	return version, err
}

type CatalogPresentationKey struct {
	HostEpoch string
	Seq       uint64
	Version   uint64
}

func (r *Registry) ReadCatalogPresentationKey(ctx context.Context) (CatalogPresentationKey, error) {
	tx, err := r.read.BeginTx(ctx, nil)
	if err != nil {
		return CatalogPresentationKey{}, err
	}
	defer tx.Rollback()
	key := CatalogPresentationKey{}
	if key.HostEpoch, err = catalogHostEpochTx(tx, false); err != nil {
		return key, err
	}
	var seq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM catalog_events`).Scan(&seq); err != nil {
		return key, err
	}
	key.Seq = uint64(seq.Int64)
	if key.Version, err = presentationVersionTx(ctx, tx); err != nil {
		return key, err
	}
	return key, tx.Commit()
}
