package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	extensionGrantYes = "approve"
	extensionGrantNo  = "decline"
)

// An extensionGrant is what an agent asked the person to approve. It holds
// the manifest or upstream the person was shown, so an answer applies only
// to that and never to whatever the folder holds by then.
type extensionGrant struct {
	SessionID string             `json:"sessionId"`
	Kind      string             `json:"kind"`
	ID        string             `json:"id"`
	Manifest  extension.Manifest `json:"manifest,omitzero"`
	Mount     string             `json:"mount,omitempty"`
	Upstream  string             `json:"upstream,omitempty"`
	Key       *proxyacct.KeyRef  `json:"key,omitempty"`
}

type extensionGrants struct {
	path string
	mu   sync.Mutex
}

func (g *extensionGrants) read() (map[string]extensionGrant, error) {
	data, err := os.ReadFile(g.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]extensionGrant{}, nil
	}
	if err != nil {
		return nil, err
	}
	grants := map[string]extensionGrant{}
	return grants, json.Unmarshal(data, &grants)
}

func (g *extensionGrants) write(grants map[string]extensionGrant) error {
	data, err := json.MarshalIndent(grants, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(g.path, append(data, '\n'), 0o600)
}

// askPersonToGrant refuses an approve or key run from inside a session and
// raises an ask the person answers with one tap instead. The same request
// while its ask is open gets that ask back.
func (d *Daemon) askPersonToGrant(sessionID, kind, id, mountName string, key *proxyacct.KeyRef) error {
	session, err := d.Registry.Session(strings.TrimSpace(sessionID))
	if err != nil {
		return actionFailed(http.StatusForbidden, "ask_the_person", "Only the person approves extensions and names keys; ask them.")
	}
	ext, err := d.onDisk(id)
	if err != nil {
		return invalidRequest(err.Error())
	}
	grant := extensionGrant{SessionID: session.ID, Kind: kind, ID: id}
	var question, does string
	switch kind {
	case "approve":
		grant.Manifest = ext.Manifest
		waits := d.extensionWaitsFor(id)
		if waits == "" {
			return invalidRequest("extension " + id + " has nothing waiting for approval")
		}
		question = fmt.Sprintf("Approve extension %s? It gets %s", id, waits)
		does = "Grant what " + id + "/extension.json declares now"
	case "key":
		index := slices.IndexFunc(ext.Manifest.Mounts, func(mount extension.Mount) bool { return mount.Name == mountName })
		if index < 0 {
			return invalidRequest("extension " + id + " declares no mount " + mountName)
		}
		mount := ext.Manifest.Mounts[index]
		if key != nil {
			if err := key.Validate(); err != nil {
				return invalidRequest("the key " + err.Error())
			}
		}
		grant.Mount, grant.Upstream, grant.Key = mount.Name, mount.Upstream, key
		if key == nil {
			question = fmt.Sprintf("Forget the key of %s's mount %s?", id, mount.Name)
		} else {
			question = fmt.Sprintf("Send %s to %s for %s's mount %s?", key.Describe(), mount.Upstream, id, mount.Name)
		}
		if len([]rune(question)) > registry.MaxAskQuestion {
			return actionFailed(http.StatusForbidden, "ask_the_person", "Only the person names keys, and this one is too long to show in an ask; ask them to run the command from their own shell.")
		}
		does = "Agenthail reads it when a request needs it"
	}
	g := d.extensions.grants
	g.mu.Lock()
	defer g.mu.Unlock()
	grants, err := g.read()
	if err != nil {
		return err
	}
	for askID, held := range grants {
		if !reflect.DeepEqual(held, grant) {
			continue
		}
		if ask, found, err := d.Registry.Ask(askID); err == nil && found && ask.State == registry.AskOpen {
			return actionFailed(http.StatusForbidden, "ask_the_person", "Only the person can do this. They were already asked ("+askID+").")
		}
	}
	title := fmt.Sprintf("%s asks you to approve %s", d.resolveDisplay(session.ID), id)
	if kind == "key" {
		title = fmt.Sprintf("%s asks you to name a key for %s", d.resolveDisplay(session.ID), id)
	}
	ask, err := d.Registry.RaiseAsk(registry.Ask{SessionID: session.ID, Origin: registry.AskOriginExtensionGrant, Title: clipText(title, registry.MaxAskTitle), Questions: []registry.AskQuestion{{
		Question:    clipText(question, registry.MaxAskQuestion),
		Options:     []registry.AskOption{{ID: extensionGrantYes, Label: "Approve", Does: clipText(does, registry.MaxAskDoes)}, {ID: extensionGrantNo, Label: "Decline"}},
		Recommended: extensionGrantNo,
	}}, RoutedTo: registry.AskToPerson}, time.Now())
	if err != nil {
		return err
	}
	grants[ask.ID] = grant
	if err := g.write(grants); err != nil {
		return err
	}
	d.announceAsk(ask)
	if err := d.Registry.MarkAskAnnounced(ask.ID, time.Now()); err != nil {
		d.log.Printf("extensions: mark %s told: %s", ask.ID, err)
	}
	d.publishAsk(session.ID)
	return actionFailed(http.StatusForbidden, "ask_the_person", "Only the person can do this. They were asked on their devices ("+ask.ID+"); it applies when they approve.")
}

func (d *Daemon) answerExtensionGrant(ctx context.Context, ask registry.Ask, response surface.Response, p principal) (actionResult, error) {
	if p.Kind == principalSession && !p.ForPerson {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "only the person approves extensions")
	}
	if len(response.Choices) != 1 || (response.Choices[0] != extensionGrantYes && response.Choices[0] != extensionGrantNo) {
		return actionResult{}, invalidRequest("choose approve or decline")
	}
	choice := response.Choices[0]
	g := d.extensions.grants
	g.mu.Lock()
	defer g.mu.Unlock()
	grants, err := g.read()
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	grant, ok := grants[ask.ID]
	if !ok {
		return actionResult{}, staleRequest()
	}
	if choice == extensionGrantYes {
		if err := d.applyExtensionGrant(grant); err != nil {
			return actionResult{}, conflict(err.Error())
		}
	}
	claimed, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: map[string]string{ask.Questions[0].Question: choice}}, registry.AskToPerson, p.actor(), time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return actionResult{}, staleRequest()
	}
	delete(grants, ask.ID)
	if err := g.write(grants); err != nil {
		d.log.Printf("extensions: forget answered grant %s: %s", ask.ID, err)
	}
	if err := d.Registry.RecordAskApplied(ask.ID, "applied", "", time.Now()); err != nil {
		d.log.Printf("extensions: record the answer to %s: %s", ask.ID, err)
	}
	d.sendWithdraws()
	d.publishAsk(ask.SessionID)
	outcome := "declined"
	if choice == extensionGrantYes {
		outcome = "approved"
		d.background.start(func(ctx context.Context) {
			if err := d.syncExtensionMounts(); err != nil {
				d.log.Printf("extensions: sync mounts after %s: %s", ask.ID, err)
			}
			if grant.Kind == "approve" {
				if err := d.reloadExtensions(ctx); err != nil && !errors.Is(err, errExtensionHostDown) {
					d.log.Printf("extensions: reload after %s: %s", ask.ID, err)
				}
			}
		})
	}
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": outcome}}), nil
}

// The folder may have changed since the ask was raised; the person approved
// what they were shown, so anything else is refused.
func (d *Daemon) applyExtensionGrant(grant extensionGrant) error {
	ext, err := d.onDisk(grant.ID)
	if err != nil {
		return err
	}
	switch grant.Kind {
	case "approve":
		if !reflect.DeepEqual(ext.Manifest, grant.Manifest) {
			return errors.New(grant.ID + "/extension.json changed after you were asked; nothing was approved")
		}
		return d.extensions.approvals.Grant(grant.ID, grant.Manifest)
	case "key":
		index := slices.IndexFunc(ext.Manifest.Mounts, func(mount extension.Mount) bool { return mount.Name == grant.Mount })
		if index < 0 || ext.Manifest.Mounts[index].Upstream != grant.Upstream {
			return errors.New("the mount " + grant.Mount + " of " + grant.ID + " changed after you were asked; no key was named")
		}
		return d.extensions.approvals.SetKey(grant.ID, ext.Manifest.Mounts[index], grant.Key)
	}
	return errors.New("unknown grant " + grant.Kind)
}
