package surfaces

import (
	"errors"
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func tarEntries(t *testing.T, data []byte) []string {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(data))
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	sort.Strings(names)
	return names
}

func TestAMovedConversationIsArchivedOutOfReachAndRestoredWhereItWas(t *testing.T) {
	fixture := claudetest.New(t)
	transcript := fixture.Transcript(fixtureConversation, fixture.Home)
	project := filepath.Dir(transcript)
	writeFixtureFile(t, filepath.Join(project, fixtureConversation, "subagents", "agent-a.jsonl"), "{}\n")
	writeFixtureFile(t, filepath.Join(fixture.Root, "tasks", fixtureConversation, "1.json"), `{"id":"1"}`)
	c := NewClaude(nil, fixture.Home)

	if err := c.ArchiveMove(fixtureConversation, fixtureMove); err != nil {
		t.Fatal(err)
	}
	if err := c.ArchiveMove(fixtureConversation, fixtureMove); err != nil {
		t.Fatalf("archiving again after a crash: %v", err)
	}
	roots, _ := surface.ClaudeConfigRoots(fixture.Home)
	if found := c.transcripts.pass(roots)(fixtureConversation); len(found) != 0 {
		t.Fatalf("an archived conversation is still found: %v", found)
	}
	if root, err := c.conversationRoot(fixtureConversation); err != nil || root != "" {
		t.Fatalf("an archived conversation still has a root %q: %v", root, err)
	}
	for _, gone := range []string{transcript, filepath.Join(project, fixtureConversation), filepath.Join(fixture.Root, "tasks", fixtureConversation)} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s is still in reach: %v", gone, err)
		}
	}
	var archive bytes.Buffer
	if err := c.WriteMoveArchive(&archive, fixtureConversation, fixtureMove); err != nil {
		t.Fatal(err)
	}
	want := []string{fixtureConversation + "/", fixtureConversation + "/subagents/", fixtureConversation + "/subagents/agent-a.jsonl", fixtureConversation + ".jsonl", "tasks/" + fixtureConversation + "/", "tasks/" + fixtureConversation + "/1.json"}
	sort.Strings(want)
	if got := tarEntries(t, archive.Bytes()); !slices.Equal(got, want) {
		t.Fatalf("archive holds %v, want %v", got, want)
	}

	if err := c.RestoreMove(fixtureConversation, fixtureMove); err != nil {
		t.Fatal(err)
	}
	for _, back := range []string{transcript, filepath.Join(project, fixtureConversation, "subagents", "agent-a.jsonl"), filepath.Join(fixture.Root, "tasks", fixtureConversation, "1.json")} {
		if _, err := os.Stat(back); err != nil {
			t.Fatalf("%s was not restored: %v", back, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.Root, claudeMovedDir, fixtureConversation, fixtureMove)); !os.IsNotExist(err) {
		t.Fatalf("the archive outlived the restore: %v", err)
	}
	if found := c.transcripts.pass(roots)(fixtureConversation); len(found) != 1 {
		t.Fatalf("a restored conversation is found %d times", len(found))
	}
}

type tarPart struct {
	name     string
	body     string
	typeflag byte
	link     string
}

func buildArchive(t *testing.T, parts ...tarPart) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, part := range parts {
		typeflag := part.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: part.name, Typeflag: typeflag, Mode: 0600, Linkname: part.link}
		if typeflag == tar.TypeReg {
			header.Size = int64(len(part.body))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(part.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func digest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func TestAMoveInTakesOnlyTheConversationsOwnFilesAsDeclared(t *testing.T) {
	const body = "{\"type\":\"user\"}\n"
	conversation := fixtureConversation
	transcript := tarPart{name: conversation + ".jsonl", body: body}
	cases := map[string]struct {
		parts      []tarPart
		size       int64
		sum        string
		partsLimit int64
		want       string
	}{
		"absolute path":       {parts: []tarPart{transcript, {name: "/etc/agenthail", body: "x"}}, want: "unsafe path"},
		"parent path":         {parts: []tarPart{transcript, {name: "../escape", body: "x"}}, want: "unsafe path"},
		"inner parent path":   {parts: []tarPart{transcript, {name: conversation + "/../../escape", body: "x"}}, want: "unsafe path"},
		"symlink":             {parts: []tarPart{transcript, {name: conversation + "/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}}, want: "not a regular file"},
		"hardlink":            {parts: []tarPart{transcript, {name: conversation + "/link", typeflag: tar.TypeLink, link: conversation + ".jsonl"}}, want: "not a regular file"},
		"another file":        {parts: []tarPart{transcript, {name: "settings.json", body: "{}"}}, want: "not part of conversation"},
		"another task list":   {parts: []tarPart{transcript, {name: "tasks/" + uuid.NewString() + "/1.json", body: "{}"}}, want: "not part of conversation"},
		"transcript a folder": {parts: []tarPart{{name: conversation + ".jsonl/", typeflag: tar.TypeDir}}, want: "not part of conversation"},
		"no transcript":       {parts: []tarPart{{name: conversation + "/subagents/a.jsonl", body: "{}"}}, want: "no transcript"},
		"wrong size":          {parts: []tarPart{transcript}, size: int64(len(body)) + 1, want: "move declared"},
		"wrong digest":        {parts: []tarPart{transcript}, sum: digest("other"), want: "move declared"},
		"other files too big": {parts: []tarPart{transcript, {name: conversation + "/tool-results/r.txt", body: "too long"}}, partsLimit: 3, want: "more than the 3 bytes"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := claudetest.New(t)
			c := NewClaude(nil, fixture.Home)
			cwd := filepath.Join(fixture.Home, "work")
			size, sum := int64(len(body)), digest(body)
			if test.size != 0 {
				size = test.size
			}
			if test.sum != "" {
				sum = test.sum
			}
			limit := int64(1 << 20)
			if test.partsLimit != 0 {
				limit = test.partsLimit
			}
			in := surface.MoveIn{MoveID: uuid.NewString(), Conversation: conversation, Cwd: cwd, Size: size, SHA256: sum, PartsSize: limit}
			_, err := c.ReceiveMove(context.Background(), in, bytes.NewReader(buildArchive(t, test.parts...)), admitMove)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want %q", err, test.want)
			}
			entries, _ := os.ReadDir(fixture.Root)
			for _, entry := range entries {
				if entry.Name() == "projects" || entry.Name() == "tasks" || strings.HasPrefix(entry.Name(), ".agenthail-move-") {
					t.Fatalf("a refused move left %s in the root", entry.Name())
				}
			}
			if _, err := os.Stat("/etc/agenthail"); err == nil {
				t.Fatal("an absolute archive path was written")
			}
		})
	}
}

func TestAMoveInRefusesWhatClaudeWouldRenameOrADivergedCopy(t *testing.T) {
	fixture := claudetest.New(t)
	c := NewClaude(nil, fixture.Home)
	long := "/" + strings.Repeat("a", claudeProjectDirLimit)
	if err := c.CheckMoveIn(surface.MoveIn{Conversation: fixtureConversation, Cwd: long, Size: 10, SHA256: digest("x")}); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("long cwd err=%v", err)
	}
	cwd := filepath.Join(fixture.Home, "work")
	dest := filepath.Join(fixture.Root, "projects", claudeProjectDirName(cwd), fixtureConversation+".jsonl")
	writeFixtureFile(t, dest, "turn one\nturn two\n")
	if err := c.CheckMoveIn(surface.MoveIn{Conversation: fixtureConversation, Cwd: cwd, Size: 4}); err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("shorter incoming copy err=%v", err)
	}
	elsewhere := filepath.Join(fixture.Root, "projects", "other-project", fixtureConversation+".jsonl")
	writeFixtureFile(t, elsewhere, "x\n")
	if err := c.CheckMoveIn(surface.MoveIn{Conversation: fixtureConversation, Cwd: cwd, Size: 100}); err == nil || !strings.Contains(err.Error(), "already has a transcript here") {
		t.Fatalf("copy in another project err=%v", err)
	}
	os.Remove(elsewhere)
	diverged := "turn one\nturn 2\nturn three\n"
	in := surface.MoveIn{MoveID: uuid.NewString(), Conversation: fixtureConversation, Cwd: cwd, Size: int64(len(diverged)), SHA256: digest(diverged)}
	if _, err := c.ReceiveMove(context.Background(), in, bytes.NewReader(buildArchive(t, tarPart{name: fixtureConversation + ".jsonl", body: diverged})), admitMove); err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("diverged copy err=%v", err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "turn one\nturn two\n" {
		t.Fatalf("a refused move changed the copy here: %q", data)
	}
}

func TestAMovedConversationResumesInTheHostWithItsModelAndMode(t *testing.T) {
	_, host, c := hostResume(t)
	conversation := uuid.NewString()
	body := `{"type":"user","sessionId":"` + conversation + `","cwd":"/elsewhere","message":{"role":"user","content":"hello"}}` + "\n"
	archive := buildArchive(t,
		tarPart{name: conversation + ".jsonl", body: body},
		tarPart{name: conversation + "/", typeflag: tar.TypeDir},
		tarPart{name: conversation + "/tool-results/r.txt", body: "result"},
		tarPart{name: "tasks/", typeflag: tar.TypeDir},
		tarPart{name: "tasks/" + conversation + "/1.json", body: `{"id":"1"}`},
	)
	in := surface.MoveIn{MoveID: uuid.NewString(), Conversation: conversation, Cwd: host.Cwd, Model: "claude-opus-4-8", PermissionMode: "plan", Title: "moved work", Size: int64(len(body)), SHA256: digest(body), PartsSize: int64(len("result") + len(`{"id":"1"}`))}
	session, err := c.ReceiveMove(context.Background(), in, bytes.NewReader(archive), admitMove)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != conversation || session.RuntimeKind != surface.RuntimeClaudeHost || session.PID <= 0 {
		t.Fatalf("session=%+v", session)
	}
	root := filepath.Join(host.Home, ".claude")
	placed := filepath.Join(root, "projects", claudeProjectDirName(host.Cwd))
	for _, path := range []string{filepath.Join(placed, conversation+".jsonl"), filepath.Join(placed, conversation, "tool-results", "r.txt"), filepath.Join(root, "tasks", conversation, "1.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was not placed: %v", path, err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(placed, conversation+".jsonl"))
	if !strings.HasPrefix(string(data), body) {
		t.Fatalf("placed transcript does not start with the moved one: %q", data)
	}
	spawns := host.Spawns(t, conversation)
	if len(spawns) != 1 {
		t.Fatalf("spawns=%+v", spawns)
	}
	argv := strings.Join(spawns[0].Argv, " ")
	for _, want := range []string{"--resume " + conversation, "claude-opus-4-8", "plan"} {
		if !strings.Contains(argv, want) && !strings.Contains(argv, strings.Replace(want, " ", "=", 1)) {
			t.Fatalf("resume argv %q lacks %q", argv, want)
		}
	}
	if _, running, err := c.conversationProcess(context.Background(), conversation); err != nil || !running {
		t.Fatalf("running=%v err=%v", running, err)
	}
	if err := c.DropMoveIn(context.Background(), conversation, in.MoveID, func() error { return nil }); !errors.Is(err, surface.ErrMoveInRunning) {
		t.Fatalf("dropping a move in Claude already runs: %v", err)
	}
	again, err := c.ReceiveMove(context.Background(), in, bytes.NewReader(archive), admitMove)
	if err != nil || again.PID != session.PID {
		t.Fatalf("a repeated move in of a running conversation: session=%+v err=%v", again, err)
	}
	if spawns := host.Spawns(t, conversation); len(spawns) != 1 {
		t.Fatalf("a repeated move in started Claude again: %+v", spawns)
	}
}

func TestPreparingAMoveClosesTheHostSessionAndDeclaresItsTranscript(t *testing.T) {
	_, host, c := hostResume(t)
	session, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd, PermissionMode: "acceptEdits"})
	if err != nil {
		t.Fatal(err)
	}
	waitForTranscript(t, c, session.ID)
	out, err := c.PrepareMove(context.Background(), session, surface.AdoptRequest{Wait: claudeAdoptStopTimeout})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Release()
	if out.Stopped != "host" || out.PermissionMode != "acceptEdits" || out.Cwd != host.Cwd {
		t.Fatalf("out=%+v", out)
	}
	roots, _ := surface.ClaudeConfigRoots(host.Home)
	paths := c.transcripts.pass(roots)(session.ID)
	if len(paths) != 1 {
		t.Fatalf("transcripts=%v", paths)
	}
	size, sum, err := hashFile(paths[0])
	if err != nil || size != out.Size || sum != out.SHA256 {
		t.Fatalf("declared %d/%s, file is %d/%s (%v)", out.Size, out.SHA256, size, sum, err)
	}
	if _, running, _ := c.conversationProcess(context.Background(), session.ID); running {
		t.Fatal("a prepared move left a process holding the conversation")
	}
}

const fixtureMove = "4d3c2b1a-0000-4000-8000-00000000a001"

func admitMove() error { return nil }

func TestASecondMoveOfAConversationIgnoresTheCopyAnEarlierMoveLeft(t *testing.T) {
	fixture := claudetest.New(t)
	transcript := fixture.Transcript(fixtureConversation, fixture.Home)
	c := NewClaude(nil, fixture.Home)
	if err := c.ArchiveMove(fixtureConversation, fixtureMove); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(transcript), 0700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, transcript, "{\"type\":\"user\"}\n{\"type\":\"assistant\"}\n")

	second := "4d3c2b1a-0000-4000-8000-00000000a002"
	if err := c.ArchiveMove(fixtureConversation, second); err != nil {
		t.Fatalf("a second move after a finished one: %v", err)
	}
	if err := c.RestoreMove(fixtureConversation, second); err != nil {
		t.Fatalf("putting back a second move that did not happen: %v", err)
	}
	data, err := os.ReadFile(transcript)
	if err != nil || !strings.Contains(string(data), "assistant") {
		t.Fatalf("the second move's own transcript did not come back: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.Root, claudeMovedDir, fixtureConversation, fixtureMove, fixtureConversation+".jsonl")); err != nil {
		t.Fatalf("the first move's recovery copy is gone: %v", err)
	}
}

func TestAMoveInClaudeRefusesLeavesNoCopyItCouldResume(t *testing.T) {
	_, host, c := hostResume(t)
	conversation := uuid.NewString()
	body := `{"type":"user","sessionId":"` + conversation + `","cwd":"/elsewhere","message":{"role":"user","content":"hello"}}` + "\n"
	in := surface.MoveIn{MoveID: uuid.NewString(), Conversation: conversation, Cwd: host.Cwd, Size: int64(len(body)), SHA256: digest(body), PartsSize: 2}
	host.Refuse(t, conversation, "not logged in")
	_, err := c.ReceiveMove(context.Background(), in, bytes.NewReader(buildArchive(t, tarPart{name: conversation + ".jsonl", body: body}, tarPart{name: "tasks/" + conversation + "/1.json", body: "{}"})), admitMove)
	if err == nil || errors.Is(err, surface.ErrMoveInPlaced) || errors.Is(err, surface.ErrMoveInRunning) {
		t.Fatalf("a move in Claude refused: %v", err)
	}
	roots, _ := surface.ClaudeConfigRoots(host.Home)
	if found := c.transcripts.pass(roots)(conversation); len(found) != 0 {
		t.Fatalf("a refused move in left %v where Claude finds it", found)
	}
	if _, err := os.Stat(filepath.Join(host.Home, ".claude", "tasks", conversation)); !os.IsNotExist(err) {
		t.Fatalf("a refused move in left its tasks in place: %v", err)
	}
	committed := false
	if err := c.DropMoveIn(context.Background(), conversation, in.MoveID, func() error { committed = true; return nil }); err != nil || !committed {
		t.Fatalf("dropping a refused move in: committed=%v err=%v", committed, err)
	}
}

func TestAMoveInItsAbortReachedFirstPlacesNothing(t *testing.T) {
	_, host, c := hostResume(t)
	conversation := uuid.NewString()
	body := `{"type":"user","sessionId":"` + conversation + `"}` + "\n"
	in := surface.MoveIn{MoveID: uuid.NewString(), Conversation: conversation, Cwd: host.Cwd, Size: int64(len(body)), SHA256: digest(body)}
	_, err := c.ReceiveMove(context.Background(), in, bytes.NewReader(buildArchive(t, tarPart{name: conversation + ".jsonl", body: body})), func() error { return errors.New("the move is aborted") })
	if err == nil {
		t.Fatal("an aborted move was received")
	}
	roots, _ := surface.ClaudeConfigRoots(host.Home)
	if found := c.transcripts.pass(roots)(conversation); len(found) != 0 || len(host.Spawns(t, conversation)) != 0 {
		t.Fatalf("an aborted move placed %v", found)
	}
}

func waitForTranscript(t *testing.T, c *Claude, conversation string) {
	t.Helper()
	claudehosttest.WaitFor(t, "the conversation's transcript", func() bool {
		roots, _ := surface.ClaudeConfigRoots(c.home)
		return len(c.transcripts.pass(roots)(conversation)) == 1
	})
}
