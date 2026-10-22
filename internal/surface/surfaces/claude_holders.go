package surfaces

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// holderRecords returns the session records of the live processes that hold
// conversation, earliest started first.
func holderRecords(records []claudeSessionRecord, conversation string) []map[string]any {
	var out []map[string]any
	for _, record := range records {
		if str(record.fields, "sessionId") != conversation {
			continue
		}
		if pid, _ := record.fields["pid"].(float64); pid <= 0 {
			continue
		}
		out = append(out, record.fields)
	}
	sort.SliceStable(out, func(i, j int) bool { return recordStartedAt(out[i]) < recordStartedAt(out[j]) })
	return out
}

func recordStartedAt(record map[string]any) float64 {
	if startedAt, ok := record["startedAt"].(float64); ok {
		return startedAt
	}
	return float64(time.Now().UnixMilli())
}

func recordPID(record map[string]any) int {
	pid, _ := record["pid"].(float64)
	return int(pid)
}

// sessionHolders describes each holder for a listing or a refusal.
func sessionHolders(records []map[string]any) []surface.Holder {
	holders := make([]surface.Holder, 0, len(records))
	for _, record := range records {
		holder := surface.Holder{PID: recordPID(record), TTY: processTTY(recordPID(record)), Status: claudePeerStatus(record)}
		if startedAt, ok := record["startedAt"].(float64); ok {
			holder.StartedAt = time.UnixMilli(int64(startedAt))
		}
		holders = append(holders, holder)
	}
	return holders
}

// processTTY names the terminal a process runs in, or "" when it has none.
func processTTY(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(output))
	if tty == "" || strings.Trim(tty, "?") == "" {
		return ""
	}
	return tty
}

// claudePin names one process among those that hold a conversation: its pid
// and, once resolved, the start it recorded, so a reused pid never matches.
type claudePin struct {
	pid   int
	start string
}

func sessionPin(sess *surface.Session) claudePin {
	return claudePin{pid: sess.HolderPID, start: sess.HolderStart}
}

func (p claudePin) matches(record map[string]any) bool {
	return recordPID(record) == p.pid && (p.start == "" || recordStart(record) == p.start)
}

// recordStart identifies the process that wrote a session record: the start
// the process recorded for itself, else when it began its session. A process
// that later reuses the pid writes a record that names neither.
func recordStart(record map[string]any) string {
	if start := str(record, "procStart"); start != "" {
		return start
	}
	if startedAt, ok := record["startedAt"].(float64); ok {
		return strconv.FormatFloat(startedAt, 'f', -1, 64)
	}
	return ""
}

func (p claudePin) missing(conversation string) error {
	return surface.DeliveryTerminal(fmt.Errorf("Claude process %d no longer holds conversation %s", p.pid, conversation), surface.DeliveryTargetMissing)
}

// ensureOneHolder refuses a write to a conversation that several live
// processes hold unless the session names one of them, and a write naming a
// process that no longer holds the conversation.
func (c *Claude) ensureOneHolder(sess *surface.Session) error {
	_, records, err := c.records()
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	holders := holderRecords(records, sess.ID)
	if pin := sessionPin(sess); pin.pid > 0 {
		for _, record := range holders {
			if pin.matches(record) {
				return nil
			}
		}
		return pin.missing(sess.ID)
	}
	if len(holders) > 1 {
		return refuseSharedConversation(sess.ID, holders)
	}
	return nil
}

func refuseSharedConversation(conversation string, holders []map[string]any) error {
	return surface.DeliveryRefused(surface.SharedConversationError{SessionID: conversation, Holders: sessionHolders(holders)})
}

// refuseSharedAdoption refuses to open a conversation several processes hold
// in the Claude host, pinned or not: the processes left open would still
// hold it, so only closing them makes the host its one holder.
func refuseSharedAdoption(conversation string, holders []map[string]any) error {
	shared := surface.SharedConversationError{SessionID: conversation, Holders: sessionHolders(holders)}
	return surface.DeliveryRefused(fmt.Errorf("%w; the Claude host can take it over only once a single terminal holds it, so close the others first", shared.Unpinnable()))
}

// shareConversation makes one session of the processes that hold a
// conversation. No process is attached to it, so a write must name one.
func shareConversation(processes []claudeProcess) claudeProcess {
	sort.SliceStable(processes, func(i, j int) bool { return processes[i].startedAt < processes[j].startedAt })
	shared := processes[0]
	shared.session.PID, shared.session.Bridge, shared.session.Transport = 0, "", ""
	shared.holders = processes
	records := make([]map[string]any, 0, len(processes))
	for _, process := range processes {
		records = append(records, process.record)
	}
	shared.session.Holders = sessionHolders(records)
	return shared
}

// applyHoldersStatus marks a shared conversation busy when any holder is.
func applyHoldersStatus(sess *surface.Session) {
	for _, holder := range sess.Holders {
		if holder.Status == surface.StatusBusy {
			sess.Status = surface.StatusBusy
			return
		}
	}
}

// pinned returns the session of process pid. Within a conversation several
// processes hold it is addressed to that process alone; a conversation one
// process holds is that process's session already.
func (p claudeProcess) pinned(pid int) (claudeProcess, bool) {
	for _, holder := range p.holders {
		if holder.session.PID == pid {
			holder.session.HolderPID, holder.session.HolderStart = pid, recordStart(holder.record)
			return holder, true
		}
	}
	if len(p.holders) == 0 && p.session.PID == pid {
		return p, true
	}
	return claudeProcess{}, false
}

// sharedLocalStatus reads a shared conversation's status from its holders'
// records. A holder that left or exited is a change of holders, which only a
// discovery pass publishes.
func (c *Claude) sharedLocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	records := make([]map[string]any, 0, len(session.Holders))
	for _, holder := range session.Holders {
		record, held := c.heldRecord(session.ID, holder)
		if !held {
			return session, fmt.Errorf("claude pid %d no longer holds session %s", holder.PID, session.ID)
		}
		records = append(records, record)
	}
	session.Holders = append([]surface.Holder(nil), session.Holders...)
	for index, record := range records {
		session.Holders[index].Status = claudePeerStatus(record)
	}
	deciding, _ := c.sharedLiveRecord(session)
	c.applyRecordStatus(ctx, &session, deciding)
	applyHoldersStatus(&session)
	return session, nil
}

func holderRoot(records []claudeSessionRecord, fields map[string]any) string {
	for _, record := range records {
		if recordPID(record.fields) == recordPID(fields) && str(record.fields, "sessionId") == str(fields, "sessionId") {
			return record.root
		}
	}
	return ""
}

// sharedLiveRecord reads a conversation several processes hold through the
// holder that decides its state: one that waits on a person, else one that
// works, else the earliest started. So the conversation is open while any
// holder runs, waits while any waits and works while any works.
func (c *Claude) sharedLiveRecord(sess surface.Session) (map[string]any, bool) {
	var chosen map[string]any
	rank := func(record map[string]any) int {
		switch str(record, "status") {
		case "waiting":
			return 2
		case "busy":
			return 1
		}
		return 0
	}
	for _, holder := range sess.Holders {
		record, held := c.heldRecord(sess.ID, holder)
		if !held {
			continue
		}
		if chosen == nil || rank(record) > rank(chosen) {
			chosen = record
		}
	}
	return chosen, chosen != nil
}

// heldRecord reads the live record of holder while it is still the process
// discovery listed: the same pid, conversation and start. A process that
// reused the pid since is a change of holders, which only discovery publishes.
func (c *Claude) heldRecord(conversation string, holder surface.Holder) (map[string]any, bool) {
	_, record, found := c.pidRecord(holder.PID)
	if !found || str(record, "sessionId") != conversation || !claudeRecordLive(record) {
		return nil, false
	}
	if !holder.StartedAt.IsZero() {
		if startedAt, ok := record["startedAt"].(float64); !ok || int64(startedAt) != holder.StartedAt.UnixMilli() {
			return nil, false
		}
	}
	return record, true
}
