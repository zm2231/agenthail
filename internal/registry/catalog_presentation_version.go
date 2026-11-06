package registry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type presentationInput struct {
	table   string
	columns string
	when    string
	changed bool
	shown   map[string]string
}

var presentationInputs = []presentationInput{
	{table: "aliases"},
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
	{table: "work_records", columns: presentationWorkColumns, when: `%[1]s.session_id<>''`},
	{table: "session_prs", columns: presentationPRColumns, when: `%[1]s.unlinked=0`, changed: true, shown: map[string]string{"read_failures": fmt.Sprintf("(%%[1]s.read_failures>=%d)", PRReadFailureLimit)}},
	{table: "catalog_retired_version"},
	{table: "session_runtime", columns: "session_id,completed_turn_id", changed: true},
	{table: "session_keep_alive", columns: "session_id,failed_at_ms,error"},
	{table: "sessions", columns: "id,name,surface,status,cwd,last_active_ms,runtime_kind,source,transport,host_id,parent_session_id,root_session_id,subagent_depth,agent_nickname,agent_role,registered_at,pinned_at_ms,snoozed_at_ms,snoozed_until_ms,snooze_wake"},
	{table: "catalog_sessions", columns: "session_id,unavailable_reason,waiting_on,background_tasks,usage_limit,turn_stop,host_queue_count,projection_generation,projection_fingerprint,misses,discovery_failures,retired,current,host_project"},
}

// Must cover every row catalogPresentationOutgoing and settledDeliveryFailure read.
const outgoingIntentRow = `(%[1]s.status IN ('failed','held') OR (%[1]s.status IN ('sent','delivered') AND EXISTS (SELECT 1 FROM delivery_intents f
	WHERE f.sender_session_id=%[1]s.sender_session_id AND f.target_session_id=%[1]s.target_session_id AND f.status='failed' AND f.id<%[1]s.id)))`

// A WHEN clause stops a column from being dropped, so changed may name only columns every supported schema has.
func (input presentationInput) triggerWhen(event string) string {
	if event == "update" && input.changed {
		var changed []string
		for column := range strings.SplitSeq(input.columns, ",") {
			value := "%[1]s." + column
			if expression, ok := input.shown[column]; ok {
				value = expression
			}
			changed = append(changed, fmt.Sprintf(value, "NEW")+" IS NOT "+fmt.Sprintf(value, "OLD"))
		}
		return " WHEN " + strings.Join(changed, " OR ")
	}
	switch {
	case input.when == "":
		return ""
	case event == "insert":
		return " WHEN " + fmt.Sprintf(input.when, "NEW")
	case event == "delete":
		return " WHEN " + fmt.Sprintf(input.when, "OLD")
	}
	return " WHEN " + fmt.Sprintf(input.when, "NEW") + " OR " + fmt.Sprintf(input.when, "OLD")
}

func catalogPresentationVersionSchema() string {
	var ddl strings.Builder
	ddl.WriteString("INSERT OR IGNORE INTO catalog_presentation_version(id,version) VALUES(1,0);\n")
	for _, input := range presentationInputs {
		for _, event := range []string{"insert", "update", "delete"} {
			name := fmt.Sprintf("catalog_presentation_version_%s_%s", input.table, event)
			on := strings.ToUpper(event)
			if event == "update" && input.columns != "" {
				on += " OF " + input.columns
			}
			fmt.Fprintf(&ddl, "DROP TRIGGER IF EXISTS %s;\nCREATE TRIGGER %s AFTER %s ON %s%s\nBEGIN\n\tUPDATE catalog_presentation_version SET version=version+1;\nEND;\n", name, name, on, input.table, input.triggerWhen(event))
		}
	}
	return ddl.String()
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
