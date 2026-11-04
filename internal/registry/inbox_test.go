package registry

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func inboxMessages(t *testing.T, r *Registry, sessionID string) map[string]InboxItem {
	t.Helper()
	page, err := r.InboxPage(InboxRequest{SessionID: sessionID, Limit: inboxPageMax})
	if err != nil {
		t.Fatal(err)
	}
	messages := map[string]InboxItem{}
	for _, item := range page.Items {
		messages[item.Message] = item
	}
	return messages
}

func TestEveryQueuedMessageIsInItsTargetsInbox(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	steps := []struct {
		message string
		queue   func(message string) error
	}{
		{"plain", func(m string) error { return r.QueueMessage("target", m) }},
		{"keyed", func(m string) error { _, err := r.QueueMessageWithKey("target", m, "ledger:readback:1"); return err }},
		{"peer", func(m string) error {
			_, err := r.QueueMessageWithOptions("target", m, "peer:target:x:1", surface.SendOptions{BusyDelivery: "queue"})
			return err
		}},
		{"lead notice", func(m string) error {
			_, err := r.QueueMessageWithOptions("target", m, "lead:1", surface.SendOptions{SourceSessionID: "sender", BusyDelivery: "steer"})
			return err
		}},
		{"dispatched", func(m string) error {
			_, _, err := r.QueueDeliveryWithIntent("target", m, "", surface.SendOptions{SourceSessionID: "sender"})
			return err
		}},
		{"person", func(m string) error {
			_, _, err := r.QueueDeliveryWithIntent("target", m, "", surface.SendOptions{Author: surface.AuthorPerson})
			return err
		}},
		{"relayed", func(m string) error {
			_, err := r.QueueRelayMessageWithOptions("target", m, "relay:1:t1", 1, surface.SendOptions{SourceSessionID: "sender"})
			return err
		}},
		{"submitted first", func(m string) error {
			intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: m, Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
			if err != nil {
				return err
			}
			_, _, err = r.QueueDeliveryUsingIntent(intent.ID, m, "", surface.SendOptions{})
			return err
		}},
	}
	for _, step := range steps {
		if err := step.queue(step.message); err != nil {
			t.Fatalf("%s: %v", step.message, err)
		}
	}
	inbox := inboxMessages(t, r, "target")
	for _, step := range steps {
		item, ok := inbox[step.message]
		if !ok || !item.Pending() || item.QueueID == 0 {
			t.Errorf("%s: queued for target but its inbox shows %+v (listed=%v)", step.message, item, ok)
		}
	}
	if got := inbox["peer"].SourceSessionID; got != surface.SystemSessionID {
		t.Errorf("a message with no sender is from %q, want Agenthail", got)
	}
	if got := inbox["lead notice"].SourceSessionID; got != "sender" {
		t.Errorf("a lead notice about sender is from %q", got)
	}
	if got := inbox["person"].SenderSessionID; got != surface.PersonSessionID {
		t.Errorf("the person's message is from %q", got)
	}
}

func TestAFailureNoticeIsInTheSendersInbox(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	if _, _, err := r.QueueDeliveryWithIntent("target", "wait", "", surface.SendOptions{SourceSessionID: "sender"}); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if notified, err := r.DeadLetterMessage(item.ID, errors.New("target rejected input")); err != nil || !notified {
		t.Fatalf("notified=%v err=%v", notified, err)
	}
	for message, inboxItem := range inboxMessages(t, r, "sender") {
		if strings.Contains(message, "target rejected input") && inboxItem.SenderSessionID == surface.SystemSessionID {
			return
		}
	}
	t.Fatal("the failure notice is not in the sender's inbox")
}

func TestOnlyTheQueueChokepointWritesMessageRows(t *testing.T) {
	insert := regexp.MustCompile(`(?is)\binsert\s+(or\s+\w+\s+)?into\s+message_queue\b`)
	allowed := map[string]bool{"enqueueMessageTx": true, "QueueGoal": true, "QueueCompact": true, "queueHostDeliveryTx": true}
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			name := "package scope"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				name = fn.Name.Name
			}
			ast.Inspect(decl, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING && insert.MatchString(literal.Value) && !allowed[name] {
					t.Errorf("%s: %s inserts into message_queue; queue messages through enqueueMessageTx so they reach the inbox", path, name)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
