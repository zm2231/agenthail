package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const turnStopNoticeWindow = 24 * time.Hour

func turnStopNoticeKey(sessionID string, stop *surface.TurnStop) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + stop.TurnID))
	return "turn-stop-" + hex.EncodeToString(sum[:16])
}

func turnStopNotice(name, target string, stop *surface.TurnStop) string {
	if stop.Refused {
		return fmt.Sprintf("[agenthail] %s stopped: the safeguards refused its conversation, and they refuse every later turn in it. Read where it got to with agenthail last %s and carry its work on in a new session.", name, target)
	}
	return fmt.Sprintf("[agenthail] %s stopped: its turn ended on an error and it will not go on by itself. %s\nRead where it got to with agenthail last %s, then send it a message to retry.", name, stop.Reason, target)
}

// noticeTurnStop tells a stopped session's lead, or the session that started
// it, once per stopped turn. A stop older than a day is history nobody is
// waiting on, so a daemon that starts after one stays quiet.
func (d *Daemon) noticeTurnStop(session surface.Session) {
	stop := session.TurnStop
	if stop == nil || stop.At.IsZero() || time.Since(stop.At) > turnStopNoticeWindow {
		return
	}
	to, ok := d.noticeRecipient(session)
	if !ok {
		return
	}
	d.turnStopNoticeMu.Lock()
	defer d.turnStopNoticeMu.Unlock()
	message := turnStopNotice(d.resolveDisplay(session.ID), d.answerTarget(session.ID), stop)
	if _, err := d.sendNotice(to, message, turnStopNoticeKey(session.ID, stop)); err != nil {
		d.log.Printf("tell the %s that %s stopped: %s", to.role, d.resolveDisplay(session.ID), err)
	}
}
