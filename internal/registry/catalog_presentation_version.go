package registry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

var presentationInputs = []struct {
	table   string
	columns string
	when    string
}{
	{"aliases", "", ""},
	{"attention_items", "", ""},
	{"limit_resumes", "", ""},
	{"delivery_intents", "", outgoingIntentRow},
	{"message_queue", "", ""},
	{"session_archive", "", ""},
	{"projects", "", ""},
	{"coordinators", "", ""},
	{"coordinator_history", "", ""},
	{"coordinator_replaced", "", ""},
	{"turn_ends", "", ""},
	{"session_runtime", "session_id,completed_turn_id", ""},
	{"session_keep_alive", "session_id,failed_at_ms,error", ""},
	{"sessions", "id,name,surface,status,cwd,last_active_ms,runtime_kind,source,transport,host_id,parent_session_id,root_session_id,subagent_depth,agent_nickname,agent_role,registered_at,pinned_at_ms,snoozed_at_ms,snoozed_until_ms,snooze_wake", ""},
	{"catalog_sessions", "session_id,unavailable_reason,waiting_on,background_tasks,usage_limit,turn_stop,host_queue_count,observed_at,projection_generation,projection_fingerprint,misses,discovery_failures,retired,current,host_project", ""},
}

// Must cover every row catalogPresentationOutgoing and settledDeliveryFailure read.
const outgoingIntentRow = `(%[1]s.status IN ('failed','held') OR (%[1]s.status IN ('sent','delivered') AND EXISTS (SELECT 1 FROM delivery_intents f
	WHERE f.sender_session_id=%[1]s.sender_session_id AND f.target_session_id=%[1]s.target_session_id AND f.status='failed' AND f.id<%[1]s.id)))`

func presentationTriggerWhen(columns, when, event string) string {
	if event == "update" && columns != "" {
		var changed []string
		for column := range strings.SplitSeq(columns, ",") {
			changed = append(changed, fmt.Sprintf("NEW.%[1]s IS NOT OLD.%[1]s", column))
		}
		return " WHEN " + strings.Join(changed, " OR ")
	}
	switch {
	case when == "":
		return ""
	case event == "insert":
		return " WHEN " + fmt.Sprintf(when, "NEW")
	case event == "delete":
		return " WHEN " + fmt.Sprintf(when, "OLD")
	}
	return " WHEN " + fmt.Sprintf(when, "NEW") + " OR " + fmt.Sprintf(when, "OLD")
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
			fmt.Fprintf(&ddl, "DROP TRIGGER IF EXISTS %s;\nCREATE TRIGGER %s AFTER %s ON %s%s\nBEGIN\n\tUPDATE catalog_presentation_version SET version=version+1;\nEND;\n", name, name, on, input.table, presentationTriggerWhen(input.columns, input.when, event))
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
