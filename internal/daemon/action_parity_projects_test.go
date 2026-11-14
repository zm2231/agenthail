package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func parityFolder(name string) string {
	return filepath.Join(os.TempDir(), "agenthail-parity", name)
}

func withFolder(name string) paritySetup {
	return func(t *testing.T, _ *Daemon, _ *registry.Registry) {
		t.Helper()
		if err := os.MkdirAll(parityFolder(name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func withAddedProject(t *testing.T, d *Daemon, r *registry.Registry) {
	t.Helper()
	withFolder("project")(t, d, r)
	root, err := canonicalCatalogPath(parityFolder("project"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddProject(registry.ProjectRecord{ID: "p-parity", WorkspaceRoot: root, Title: "Parity", CreatedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
}

func withCoordinator(t *testing.T, d *Daemon, r *registry.Registry) {
	t.Helper()
	withAddedProject(t, d, r)
	root, _ := canonicalCatalogPath(parityFolder("project"))
	spec := registry.CoordinatorSpec{ProjectID: "p-parity", ProjectRoot: root, Name: "parity-lead", Surface: "codex", Cwd: root}
	if _, _, err := r.ReplaceCoordinator(spec, registry.CoordinatorChange{ChangedBy: "setup"}); err != nil {
		t.Fatal(err)
	}
}

func withSessionInProject(t *testing.T, d *Daemon, r *registry.Registry) {
	t.Helper()
	withAddedProject(t, d, r)
	session, err := r.Session("to")
	if err != nil {
		t.Fatal(err)
	}
	session.Cwd, _ = canonicalCatalogPath(parityFolder("project"))
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
}

func parityProjectCases() []parityCase {
	project := parityFolder("project")
	loose := parityFolder("loose")
	quote := func(value string) string { return `"` + value + `"` }
	action := func(name, action, fields string, setup paritySetup) parityCase {
		body := `{"action":"` + action + `"`
		if fields != "" {
			body += "," + fields
		}
		return parityCase{name: name, action: action, body: body + `}`, setup: setup}
	}
	long := strings.Repeat("n", projectTitleLimit+1)
	return []parityCase{
		action("project-add a folder", "project-add", `"path":`+quote(project), withFolder("project")),
		action("project-add with a title", "project-add", `"path":`+quote(project)+`,"title":"Named"`, withFolder("project")),
		action("project-add a missing folder", "project-add", `"path":`+quote(filepath.Join(project, "missing")), nil),
		action("project-add a title too long", "project-add", `"path":`+quote(project)+`,"title":"`+long+`"`, withFolder("project")),
		action("project-add again", "project-add", `"path":`+quote(project), withAddedProject),
		action("project-rename an added project", "project-rename", `"projectId":"p-parity","title":"Renamed"`, withAddedProject),
		action("project-rename to nothing", "project-rename", `"projectId":"p-parity","title":" "`, withAddedProject),
		action("project-rename too long", "project-rename", `"projectId":"p-parity","title":"`+long+`"`, withAddedProject),
		action("project-archive an added project", "project-archive", `"projectId":"p-parity"`, withAddedProject),
		action("project-unarchive an added project", "project-unarchive", `"projectId":"p-parity"`, withAddedProject),
		action("project-remove an added project", "project-remove", `"projectId":"p-parity"`, withAddedProject),
		action("project-remove without an id", "project-remove", ``, nil),
		action("project-link two repositories", "project-link", `"projectId":"repo:example.com/fork","survivor":"repo:example.com/upstream"`, nil),
		action("project-link without a survivor", "project-link", `"projectId":"repo:example.com/fork"`, nil),
		action("project-link a project with a lead", "project-link", `"projectId":"p-parity","survivor":"repo:example.com/upstream"`, withCoordinator),
		action("project-unlink a project not linked", "project-unlink", `"projectId":"repo:example.com/fork"`, nil),
		action("project-setting mcp off", "project-setting", `"projectId":"repo:example.com/upstream","setting":"mcp","value":"off"`, nil),
		action("project-setting an unknown value", "project-setting", `"projectId":"repo:example.com/upstream","setting":"mcp","value":"maybe"`, nil),
		action("project-setting without an id", "project-setting", `"setting":"mcp","value":"off"`, nil),
		action("project action unknown", "project-frob", `"projectId":"p-parity"`, withAddedProject),
		action("coordinator-new codex", "coordinator-new", `"surface":"codex","path":`+quote(project), withAddedProject),
		action("coordinator-new named", "coordinator-new", `"surface":"codex","path":`+quote(project)+`,"name":"@crew"`, withAddedProject),
		action("coordinator-new with a bad name", "coordinator-new", `"surface":"codex","path":`+quote(project)+`,"name":"has space"`, withAddedProject),
		action("coordinator-new of an unknown surface", "coordinator-new", `"surface":"notion","path":`+quote(project), withAddedProject),
		action("coordinator-new of an unconfigured surface", "coordinator-new", `"surface":"claude","path":`+quote(project), withAddedProject),
		action("coordinator-new outside a project", "coordinator-new", `"surface":"codex","path":`+quote(loose), withFolder("loose")),
		action("coordinator-new of a missing folder", "coordinator-new", `"surface":"codex","path":`+quote(filepath.Join(project, "missing")), withAddedProject),
		action("coordinator-new with a claude permission mode", "coordinator-new", `"surface":"codex","path":`+quote(project)+`,"permissionMode":"plan"`, withAddedProject),
		action("coordinator-new over a coordinator", "coordinator-new", `"surface":"codex","path":`+quote(project)+`,"name":"crew"`, withCoordinator),
		action("coordinator-new replacing a coordinator", "coordinator-new", `"surface":"codex","path":`+quote(project)+`,"name":"crew","replace":true`, withCoordinator),
		action("coordinator-new the same coordinator again", "coordinator-new", `"surface":"codex","path":`+quote(project)+`,"name":"parity-lead"`, withCoordinator),
		action("coordinator-set a session in the project", "coordinator-set", `"sessionId":"to"`, withSessionInProject),
		action("coordinator-set an unknown session", "coordinator-set", `"sessionId":"ghost"`, nil),
		action("coordinator-clear a coordinator", "coordinator-clear", `"projectId":"p-parity"`, withCoordinator),
		action("coordinator-clear without a coordinator", "coordinator-clear", `"projectId":"p-parity"`, withAddedProject),
		action("coordinator-clear without an id", "coordinator-clear", ``, nil),
		action("coordinator-send to a coordinator", "coordinator-send", `"name":"@parity-lead","message":"status?"`, withCoordinator),
		action("coordinator-send from a session", "coordinator-send", `"name":"parity-lead","message":"status?","sourceSessionId":"from"`, withCoordinator),
		action("coordinator-send from an unknown session", "coordinator-send", `"name":"parity-lead","message":"status?","sourceSessionId":"ghost"`, withCoordinator),
		action("coordinator-send as a person naming a session", "coordinator-send", `"name":"parity-lead","message":"status?","author":"person","sourceSessionId":"from"`, withCoordinator),
		action("coordinator-send without a message", "coordinator-send", `"name":"parity-lead"`, withCoordinator),
		action("coordinator-send to a project's lead", "coordinator-send", `"project":"Parity","message":"status?","sourceSessionId":"from"`, withCoordinator),
		action("coordinator-send to a project's lead by id", "coordinator-send", `"project":"p-parity","message":"status?"`, withCoordinator),
		action("coordinator-send to a project without a lead", "coordinator-send", `"project":"Parity","message":"status?"`, withAddedProject),
		action("coordinator-send to an unknown project", "coordinator-send", `"project":"nothing","message":"status?"`, withCoordinator),
		action("coordinator-send naming a coordinator and a project", "coordinator-send", `"name":"parity-lead","project":"Parity","message":"status?"`, withCoordinator),
		action("coordinator action unknown", "coordinator-frob", ``, nil),
	}
}
