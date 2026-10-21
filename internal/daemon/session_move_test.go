package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const moveRemote = "https://example.com/acme/widget.git"

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

// moveNode is a daemon with a real Claude host and a checkout of the test
// repository.
type moveNode struct {
	*federationNode
	host   *claudehosttest.Fixture
	claude *surfaces.Claude
}

func newMoveNode(t *testing.T) *moveNode {
	t.Helper()
	return moveNodeOn(t, claudehosttest.New(t, ""))
}

// moveNodeOn starts host and a daemon whose Claude sessions run in it.
func moveNodeOn(t *testing.T, host *claudehosttest.Fixture) *moveNode {
	t.Helper()
	allowLoopbackHosts(t)
	host.Start(t)
	claude := surfaces.NewClaude(nil, host.Home)
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	d := New(reg, []surface.Surface{claude})
	node := &federationNode{daemon: d, reg: reg, gate: &actionGate{}}
	node.handler = d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	return &moveNode{federationNode: node, host: host, claude: claude}
}

// movePair is a source paired with a target, the source's checkout at a
// pushed commit, and the target's checkout of the same repository added as
// a project there.
func movePair(t *testing.T) (*moveNode, *moveNode, registry.PairedHost, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	return pairMoveNodes(t, newMoveNode(t), newMoveNode(t))
}

// pairMoveNodes pairs source with target and gives them checkouts of one
// repository, the source's at a pushed commit.
func pairMoveNodes(t *testing.T, source, target *moveNode) (*moveNode, *moveNode, registry.PairedHost, string) {
	t.Helper()
	repo := source.host.Cwd
	runGit(t, repo, "init", "--quiet", "--initial-branch=main")
	runGit(t, repo, "remote", "add", "origin", moveRemote)
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("widget\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README")
	runGit(t, repo, "commit", "--quiet", "-m", "start")
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	commit := runGit(t, repo, "rev-parse", "HEAD")
	checkout := filepath.Join(target.host.Home, "checkouts", "widget")
	runGit(t, target.host.Home, "clone", "--quiet", repo, checkout)
	runGit(t, checkout, "remote", "set-url", "origin", moveRemote)
	if err := target.reg.AddProject(registry.ProjectRecord{ID: "project-widget", WorkspaceRoot: checkout, Title: "widget", Added: true}); err != nil {
		t.Fatal(err)
	}
	target.serve(t, nil)
	source.start(t, testRoots(target.server))
	record := source.pair(t, target.federationNode, "forge")
	awaitFederation(t, "the target connected", func() bool { return source.daemon.hostConnected(record.ID) })
	if head := runGit(t, checkout, "rev-parse", "HEAD"); head != commit {
		t.Fatalf("target checkout at %s, want %s", head, commit)
	}
	return source, target, record, checkout
}

func startMovable(t *testing.T, node *moveNode) *surface.Session {
	t.Helper()
	session, _, err := node.claude.StartSession(context.Background(), surface.SessionStartOptions{Message: "remember kestrel", Cwd: node.host.Cwd, PermissionMode: "acceptEdits"})
	if err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the first turn", func() bool { return len(node.host.Turns(t, session.ID)) == 1 })
	listed := hostedSession(t, node.claude, session.ID)
	if err := node.reg.RegisterSession(listed); err != nil {
		t.Fatal(err)
	}
	return &listed
}

func claudeTranscriptIn(t *testing.T, home, conversation string) string {
	t.Helper()
	found, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", conversation+".jsonl"))
	if len(found) != 1 {
		return ""
	}
	return found[0]
}

func TestAnIdleClaudeSessionMovesToAPairedHostAndRunsThere(t *testing.T) {
	source, target, record, checkout := movePair(t)
	session := startMovable(t, source)
	if err := source.reg.SetAlias("builder", session.ID); err != nil {
		t.Fatal(err)
	}
	queued, err := source.reg.QueueMessageWithOptions(session.ID, "sent while moving", "key-moving", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sourceTranscript := claudeTranscriptIn(t, source.host.Home, session.ID)
	before, err := os.ReadFile(sourceTranscript)
	if err != nil {
		t.Fatal(err)
	}

	result, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "forge"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Move.State != registry.MoveDone || result.Stopped != "host" || result.Alias != "builder" {
		t.Fatalf("result=%+v", result)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != record.ID {
		t.Fatalf("source owner=%q", owner)
	}
	if path := claudeTranscriptIn(t, source.host.Home, session.ID); path != "" {
		t.Fatalf("the source still reaches its transcript at %s", path)
	}
	archived := filepath.Join(source.host.Home, ".claude", "agenthail-moved", session.ID, result.Move.ID, session.ID+".jsonl")
	if data, err := os.ReadFile(archived); err != nil || string(data) != string(before) {
		t.Fatalf("the source's recovery copy: err=%v", err)
	}

	moved, err := target.reg.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if owner, _ := target.reg.SessionHost(session.ID); owner != "" || moved.RuntimeKind != surface.RuntimeClaudeHost || moved.PID <= 0 {
		t.Fatalf("target row=%+v owner=%q", moved, owner)
	}
	if moved.Cwd != checkout {
		t.Fatalf("the session runs in %s on the target, want its checkout %s", moved.Cwd, checkout)
	}
	if alias, _ := target.reg.LookupAlias("builder"); alias != session.ID {
		t.Fatalf("target alias=%q", alias)
	}
	placed := filepath.Join(target.host.Home, ".claude", "projects", strings.NewReplacer("/", "-", ".", "-").Replace(checkout), session.ID+".jsonl")
	data, err := os.ReadFile(placed)
	if err != nil || !strings.HasPrefix(string(data), string(before)) {
		t.Fatalf("the target's transcript does not continue the source's: err=%v", err)
	}
	spawns := target.host.Spawns(t, session.ID)
	if len(spawns) != 1 || !strings.Contains(strings.Join(spawns[0].Argv, " "), "acceptEdits") {
		t.Fatalf("target spawns=%+v", spawns)
	}

	peers := claudetest.ListenPeerManager(t, target.host.Home)
	claudehosttest.WaitFor(t, "the held send to reach the target", func() bool {
		source.daemon.drainHostDeliveries(context.Background(), mustPairedHost(t, source, record.ID))
		return peers.Count("sent while moving") == 1
	})
	if item, err := source.reg.QueueItem(queued, nil); err != nil || item.Status != "delivered" {
		t.Fatalf("held send=%+v err=%v", item, err)
	}
	target.daemon.discoverCatalog(context.Background())
	awaitFederation(t, "the source to show the session as the target's", func() bool {
		for _, row := range sessionsOf(source.snapshot(t), record.ID) {
			if row.ID == record.ID+"::"+session.ID && row.UnavailableReason == "" {
				return true
			}
		}
		return false
	})
}

func mustPairedHost(t *testing.T, node *moveNode, id string) *pairedHost {
	t.Helper()
	host, ok := node.daemon.pairedHost(id)
	if !ok {
		t.Fatalf("host %s is not attached", id)
	}
	return host
}

func TestAMoveTheTargetRefusesLeavesTheSessionWhereItWas(t *testing.T) {
	source, target, _, checkout := movePair(t)
	session := startMovable(t, source)
	if err := os.WriteFile(filepath.Join(checkout, "README"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, checkout, "commit", "--quiet", "-am", "elsewhere")
	sourceTranscript := claudeTranscriptIn(t, source.host.Home, session.ID)
	if err := target.reg.RemoveProject("project-widget"); err != nil {
		t.Fatal(err)
	}

	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "forge"})
	var refused MoveRefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "no checkout") {
		t.Fatalf("err=%v", err)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != "" {
		t.Fatalf("a refused move changed the owner to %q", owner)
	}
	if claudeTranscriptIn(t, source.host.Home, session.ID) != sourceTranscript {
		t.Fatal("a refused move took the transcript away")
	}
	if _, err := target.reg.Session(session.ID); err == nil {
		t.Fatal("a refused move registered the session on the target")
	}
	moves, _ := source.reg.SessionMoves(false)
	if len(moves) != 1 || moves[0].State != registry.MoveFailed {
		t.Fatalf("moves=%+v", moves)
	}
	if queued, err := source.reg.QueueMessageWithOptions(session.ID, "after the refusal", "key-after", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	} else if item, err := source.reg.ClaimNextMessage(session.ID, time.Now()); err != nil || item == nil || item.ID != queued {
		t.Fatalf("a refused move kept the queue held: %+v err=%v", item, err)
	}
}

func TestAMoveWhoseTransferIsNotAnsweredStaysUnresolvedUntilTheTargetAnswers(t *testing.T) {
	source, target, record, _ := movePair(t)
	session := startMovable(t, source)
	target.gate.drops.Store(0)
	hold := &snapshotHold{release: make(chan struct{})}
	target.gate.transfers.Store(hold)

	done := make(chan error, 1)
	go func() {
		_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "forge"})
		done <- err
	}()
	awaitFederation(t, "the transfer to reach the target", func() bool { return hold.waiting.Load() > 0 })
	target.server.CloseClientConnections()
	err := <-done
	var unknown MoveUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("err=%v", err)
	}
	move, _ := source.reg.SessionMove(unknown.MoveID)
	if move.State != registry.MoveSent {
		t.Fatalf("an unanswered move is %s", move.State)
	}
	if _, err := source.reg.QueueMessageWithOptions(session.ID, "queued while unresolved", "key-unresolved", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if item, err := source.reg.ClaimNextMessage(session.ID, time.Now()); err != nil || item != nil {
		t.Fatalf("an unresolved move let a local claim through: %+v err=%v", item, err)
	}
	if path := claudeTranscriptIn(t, source.host.Home, session.ID); path != "" {
		t.Fatalf("an unresolved move left the transcript reachable at %s", path)
	}

	target.gate.transfers.Store(nil)
	close(hold.release)
	source.daemon.resumeHostMoves(context.Background(), mustPairedHost(t, source, record.ID))
	if move, _ := source.reg.SessionMove(unknown.MoveID); move.State != registry.MoveDone {
		t.Fatalf("recovery left the move %s (%s)", move.State, move.Detail)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != record.ID {
		t.Fatalf("owner after recovery=%q", owner)
	}
	if spawns := target.host.Spawns(t, session.ID); len(spawns) != 1 {
		t.Fatalf("the target resumed the conversation %d times", len(spawns))
	}
	held, _ := source.reg.HostDeliveries(record.ID)
	if len(held) != 1 {
		t.Fatalf("the send queued while unresolved was not handed to the target: %+v", held)
	}
}

func TestAMoveSentByADaemonThatStoppedIsSettledFromTheTargetsRecord(t *testing.T) {
	for _, stage := range []string{"before the transfer", "after the target resumed"} {
		t.Run(stage, func(t *testing.T) {
			source, target, record, _ := movePair(t)
			session := startMovable(t, source)
			host := mustPairedHost(t, source, record.ID)
			out, err := source.claude.PrepareMove(context.Background(), session, surface.AdoptRequest{})
			if err != nil {
				t.Fatal(err)
			}
			checkout, err := sourceCheckout(context.Background(), out.Cwd)
			if err != nil {
				t.Fatal(err)
			}
			manifest := moveManifest{ID: uuid.NewString(), Conversation: session.ID, SourceHost: source.daemon.hostIdentity().ID, Surface: session.Surface, Checkout: checkout, Size: out.Size, SHA256: out.SHA256}
			encoded, _ := json.Marshal(manifest)
			if err := source.reg.BeginMoveOut(manifest.ID, session.ID, record.ID, encoded); err != nil {
				t.Fatal(err)
			}
			if err := source.daemon.prepareMoveOnHost(context.Background(), host, manifest); err != nil {
				t.Fatal(err)
			}
			if err := source.claude.ArchiveMove(session.ID, manifest.ID); err != nil {
				t.Fatal(err)
			}
			if err := source.reg.SetMoveState(manifest.ID, registry.MoveSent, "", registry.MovePreparing); err != nil {
				t.Fatal(err)
			}
			if stage == "after the target resumed" {
				if status, err := source.daemon.transferMove(context.Background(), host, source.claude, manifest.ID, session.ID); err != nil || status.State != registry.MoveResumed {
					t.Fatalf("status=%+v err=%v", status, err)
				}
				target.daemon.discoverCatalog(context.Background())
				awaitFederation(t, "the target's row to reach the source while the session is still local there", func() bool {
					_, hidden := host.hiddenRow(session.ID)
					return hidden
				})
			}
			out.Release()

			spawned := len(source.host.Spawns(t, session.ID))
			if sent, err := source.claude.SendHumanTurn(context.Background(), session, "sent while the move is unresolved", nil); err == nil {
				t.Fatalf("a send resumed a conversation a sent move holds: %+v pid=%d alive=%v spawns before=%d after=%d", sent, session.PID, claudehosttest.Alive(session.PID), spawned, len(source.host.Spawns(t, session.ID)))
			}
			if len(source.host.Spawns(t, session.ID)) != spawned {
				t.Fatal("a send started Claude on a conversation a sent move holds")
			}
			source.daemon.resumeHostMoves(context.Background(), host)
			if move, _ := source.reg.SessionMove(manifest.ID); move.State != registry.MoveDone {
				t.Fatalf("recovery left the move %s (%s)", move.State, move.Detail)
			}
			if spawns := target.host.Spawns(t, session.ID); len(spawns) != 1 {
				t.Fatalf("the target resumed the conversation %d times", len(spawns))
			}
			if stage == "after the target resumed" {
				awaitFederation(t, "the source to show the row the target reported before the flip", func() bool {
					for _, row := range sessionsOf(source.snapshot(t), record.ID) {
						if row.ID == record.ID+"::"+session.ID && row.UnavailableReason == "" {
							return true
						}
					}
					return false
				})
			}
		})
	}
}

func TestADaemonThatStoppedWhilePreparingAMoveFailsIt(t *testing.T) {
	source, _, record, _ := movePair(t)
	session := startMovable(t, source)
	moveID := "6a1b0c9d-1111-4222-8333-944455556666"
	manifest, _ := json.Marshal(moveManifest{ID: moveID, Conversation: session.ID, Surface: session.Surface})
	if err := source.reg.BeginMoveOut(moveID, session.ID, record.ID, manifest); err != nil {
		t.Fatal(err)
	}
	if err := source.claude.ArchiveMove(session.ID, moveID); err != nil {
		t.Fatal(err)
	}
	source.daemon.failInterruptedMoves()
	if move, _ := source.reg.SessionMove("6a1b0c9d-1111-4222-8333-944455556666"); move.State != registry.MoveFailed {
		t.Fatalf("move=%+v", move)
	}
	if path := claudeTranscriptIn(t, source.host.Home, session.ID); path == "" {
		t.Fatal("a move that never left took the transcript away")
	}
}

func TestAMoveWhoseResumeTheTargetRefusesLeavesNoCopyThere(t *testing.T) {
	source, target, _, checkout := movePair(t)
	session := startMovable(t, source)
	if err := os.WriteFile(filepath.Join(checkout, "README"), []byte("elsewhere\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, checkout, "commit", "--quiet", "-am", "elsewhere")
	target.host.Refuse(t, session.ID, "not logged in")

	result, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "forge"})
	var refused MoveRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err=%v", err)
	}
	if result.Move.State != registry.MoveFailed {
		t.Fatalf("move=%+v", result.Move)
	}
	if claudeTranscriptIn(t, source.host.Home, session.ID) == "" {
		t.Fatal("the source did not get its conversation back")
	}
	if path := claudeTranscriptIn(t, target.host.Home, session.ID); path != "" {
		t.Fatalf("the target kept a copy it could resume at %s", path)
	}
	if moved, _ := target.reg.SessionMove(result.Move.ID); moved.State != registry.MoveAborted {
		t.Fatalf("target move=%+v", moved)
	}
	if _, err := os.Stat(moveWorktreePath(checkout, result.Move.ID)); !os.IsNotExist(err) {
		t.Fatalf("the worktree the aborted move made is still there: %v", err)
	}
}

func TestOnlyAPairedHostCanMoveASessionIn(t *testing.T) {
	_, target, _, _ := movePair(t)
	for _, path := range []string{"/api/v1/session-move?id=x", "/api/v1/session-move/prepare", "/api/v1/session-move/transfer?id=x", "/api/v1/session-move/abort?id=x"} {
		method := "POST"
		if strings.HasPrefix(path, "/api/v1/session-move?") {
			method = "GET"
		}
		if response := target.request(t, method, path, map[string]any{}); response.Code != 403 {
			t.Fatalf("%s %s answered %d to a client that is not a host", method, path, response.Code)
		}
	}
}
