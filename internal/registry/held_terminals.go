package registry

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const DeliveryShared = "shared"

const terminalOptionPrefix = "pid-"

func heldHolders(cause error) (string, error) {
	var shared surface.SharedConversationError
	if !errors.As(cause, &shared) {
		return "[]", nil
	}
	data, err := json.Marshal(shared.Holders)
	return string(data), err
}

// holdersKey names one set of terminals, so a held message asks again when
// the terminals that hold its conversation change.
func holdersKey(holders []surface.Holder) string {
	sum := sha256.New()
	for _, holder := range holders {
		fmt.Fprintf(sum, "%d\x00%s\x00", holder.PID, holder.Start)
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

func TerminalOption(pid int) string { return terminalOptionPrefix + strconv.Itoa(pid) }

func TerminalChoice(choice string) (int, bool) {
	pid, err := strconv.Atoi(strings.TrimPrefix(choice, terminalOptionPrefix))
	return pid, strings.HasPrefix(choice, terminalOptionPrefix) && err == nil && pid > 0
}

func sharedProblem(id, refusal int64, message string, holders []surface.Holder) (string, string, AskQuestion) {
	question := AskQuestion{Question: fmt.Sprintf("Its conversation is open in %d terminals; pick the one to send it to: %q", len(holders), clip(strings.Join(strings.Fields(message), " "), maxQuotedMessage))}
	for _, holder := range holders {
		question.Options = append(question.Options, AskOption{ID: TerminalOption(holder.PID), Label: clip(holder.Label(), maxOptionLabel), Does: "send it to this terminal only"})
	}
	question.Options = append(question.Options, deliveryOption(DeliveryCancel), AskOption{ID: DeliveryDismiss, Label: "Dismiss", Does: "keep it held until one terminal is left"})
	question.Recommended = DeliveryDismiss
	return fmt.Sprintf("queue:%d:%s:%d:%s", id, DeliveryShared, refusal, holdersKey(holders)), fmt.Sprintf("Message held: open in %d terminals", len(holders)), question
}

const maxOptionLabel = 40

func (r *Registry) PinHeldMessage(id int64, pid int) error {
	return r.deliveryWrite(func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRow(`SELECT holders_json FROM message_queue WHERE id=? AND `+queueRefusedRow+` AND status='pending'`, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("queue item %d is no longer held", id)
		}
		if err != nil {
			return err
		}
		var holders []surface.Holder
		if err := json.Unmarshal([]byte(raw), &holders); err != nil {
			return err
		}
		for _, holder := range holders {
			if holder.PID == pid {
				_, err := tx.Exec(`UPDATE message_queue SET holder_pid=?,holder_start=?,refused=0,available_at_ms=0,updated_at=datetime('now') WHERE id=?`, pid, holder.Start, id)
				return err
			}
		}
		return fmt.Errorf("process %d did not hold the conversation of queue item %d", pid, id)
	})
}

func (r *Registry) UnpinMessage(id int64) error {
	_, err := r.db.Exec(`UPDATE message_queue SET holder_pid=0,holder_start='',updated_at=datetime('now') WHERE id=? AND holder_pid!=0`, id)
	return err
}
