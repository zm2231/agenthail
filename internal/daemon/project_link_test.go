package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

const (
	forkProject     = "repo:github.com/example/fork"
	upstreamProject = "repo:github.com/example/upstream"
)

func cloneWithOrigin(t *testing.T, origin, path, url string) string {
	t.Helper()
	cloneAt(t, origin, path)
	gitRun(t, path, "remote", "set-url", "origin", url)
	return path
}

func snapshotProject(t *testing.T, node *federationNode, id string) *dashboardProject {
	t.Helper()
	for _, project := range node.snapshot(t).Projects {
		if project.ID == id {
			return &project
		}
	}
	return nil
}

func linkAction(t *testing.T, node *federationNode, body map[string]string) {
	t.Helper()
	if response := node.request(t, http.MethodPost, "/api/v1/actions", body); response.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", body["action"], response.Code, response.Body)
	}
}

func linkedFederation(t *testing.T) (laptop, server *federationNode) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	origin := catalogTestRepo(t)
	server = newFederationNode(t, "server-a")
	moveSessions(t, server, cloneWithOrigin(t, origin, filepath.Join(t.TempDir(), "fork"), "https://github.com/example/fork.git"))
	server.serve(t, nil)
	laptop = newFederationNode(t, "laptop-a")
	moveSessions(t, laptop, cloneWithOrigin(t, origin, filepath.Join(t.TempDir(), "upstream"), "https://github.com/example/upstream.git"))
	laptop.serve(t, nil)
	laptop.start(t, testRoots(server.server))
	server.start(t, testRoots(laptop.server))
	laptop.pair(t, server, "server")
	server.pair(t, laptop, "laptop")
	return laptop, server
}

func TestALinkJoinsAForkIntoUpstreamOnEveryPairedHostAndAnUnlinkMadeOfflineWins(t *testing.T) {
	laptop, server := linkedFederation(t)
	testkit.Eventually(t, "the laptop suggests linking the fork it shares history with", 10*time.Second, func() bool {
		suggested := map[string]string{}
		for _, project := range laptop.snapshot(t).Projects {
			for _, same := range project.SameHistory {
				suggested[project.ID] = same.ID
			}
		}
		return len(suggested) == 1 && (suggested[forkProject] == upstreamProject || suggested[upstreamProject] == forkProject)
	})

	linkAction(t, laptop, map[string]string{"action": "project-link", "projectId": forkProject, "survivor": upstreamProject})
	for name, node := range map[string]*federationNode{"laptop": laptop, "server": server} {
		testkit.Eventually(t, name+" lists the fork's checkout under upstream", 10*time.Second, func() bool {
			node.daemon.discoverCatalog(context.Background())
			project := snapshotProject(t, node, upstreamProject)
			return project != nil && len(project.Checkouts) == 2 && len(project.Linked) == 1 && project.Linked[0].ID == forkProject && snapshotProject(t, node, forkProject) == nil
		})
	}
	found, err := server.daemon.sessionProject(server.fake.sessions["server-a"])
	if err != nil || found == nil || found.ID != upstreamProject || found.LinkedFrom == nil || found.LinkedFrom.ID != forkProject {
		t.Fatalf("the server's fork session records its ledger and lead under %+v err=%v", found, err)
	}

	for _, node := range []*federationNode{laptop, server} {
		node.gate.proxyDown.Store(true)
		node.server.CloseClientConnections()
	}
	linkAction(t, server, map[string]string{"action": "project-unlink", "projectId": forkProject})
	laptop.daemon.discoverCatalog(context.Background())
	if project := snapshotProject(t, laptop, upstreamProject); project == nil || len(project.Linked) != 1 {
		t.Fatalf("the laptop dropped the link while the server was out of reach: %+v", project)
	}
	laptop.gate.proxyDown.Store(false)
	server.gate.proxyDown.Store(false)
	for name, node := range map[string]*federationNode{"laptop": laptop, "server": server} {
		testkit.Eventually(t, name+" takes the later unlink", 15*time.Second, func() bool {
			node.daemon.discoverCatalog(context.Background())
			fork, upstream := snapshotProject(t, node, forkProject), snapshotProject(t, node, upstreamProject)
			return fork != nil && upstream != nil && len(upstream.Linked) == 0
		})
	}
}

func namedLead(t *testing.T, node *federationNode, project, name string) {
	t.Helper()
	setLead(t, node, project, t.TempDir(), name, "", registry.CoordinatorChange{})
}

func clearLead(t *testing.T, node *federationNode, project string) {
	t.Helper()
	if _, err := node.reg.ClearCoordinator(project, "test", time.Time{}); err != nil {
		t.Fatal(err)
	}
	node.daemon.flushCatalog("coordinator " + project)
}

func leadHistory(t *testing.T, node *federationNode, project string) []string {
	t.Helper()
	history, err := node.reg.CoordinatorHistory(project)
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	for _, entry := range history {
		entries = append(entries, entry.Action+":"+entry.Name)
	}
	return entries
}

func linkHonoured(t *testing.T, node *federationNode) bool {
	t.Helper()
	node.daemon.discoverCatalog(context.Background())
	upstream := snapshotProject(t, node, upstreamProject)
	return upstream != nil && len(upstream.Linked) == 1 && snapshotProject(t, node, forkProject) == nil
}

func TestAForkWithPastLeadsJoinsUpstreamAndItsLeadHistoryFollowsOnEveryHost(t *testing.T) {
	laptop, server := linkedFederation(t)
	namedLead(t, laptop, forkProject, "laptop-fork-lead")
	clearLead(t, laptop, forkProject)
	namedLead(t, server, forkProject, "server-fork-lead")
	clearLead(t, server, forkProject)
	namedLead(t, laptop, upstreamProject, "upstream-lead")
	if err := ledger.NewStore(laptop.reg.WriteDB(), laptop.reg.ReadDB()).Enable(upstreamProject, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the link is taken once the laptop no longer mirrors the server's cleared fork lead", 10*time.Second, func() bool {
		response := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-link", "projectId": forkProject, "survivor": upstreamProject})
		return response.Code == http.StatusOK
	})
	want := map[*federationNode][]string{
		laptop: {"set:laptop-fork-lead", "cleared:laptop-fork-lead", "set:upstream-lead"},
		server: {"set:server-fork-lead", "cleared:server-fork-lead"},
	}
	for node, entries := range want {
		testkit.Eventually(t, "the link is honoured and the fork's past leads are upstream's", 15*time.Second, func() bool {
			return linkHonoured(t, node) && len(leadHistory(t, node, forkProject)) == 0 && slices.Equal(leadHistory(t, node, upstreamProject), entries)
		})
	}

	linkAction(t, laptop, map[string]string{"action": "project-unlink", "projectId": forkProject})
	for node, entries := range want {
		testkit.Eventually(t, "the unlink reaches the host and the moved history stays upstream's", 15*time.Second, func() bool {
			node.daemon.discoverCatalog(context.Background())
			upstream := snapshotProject(t, node, upstreamProject)
			return upstream != nil && len(upstream.Linked) == 0 && snapshotProject(t, node, forkProject) != nil && len(leadHistory(t, node, forkProject)) == 0 && slices.Equal(leadHistory(t, node, upstreamProject), entries)
		})
	}
	linkAction(t, laptop, map[string]string{"action": "project-link", "projectId": forkProject, "survivor": upstreamProject})
	testkit.Eventually(t, "the fork links again", 15*time.Second, func() bool { return linkHonoured(t, laptop) && linkHonoured(t, server) })
}

func TestAHostHoldsALinkWhileTheForkHasALeadThereAndReleasesItWhenTheLeadIsCleared(t *testing.T) {
	laptop, server := linkedFederation(t)
	for _, node := range []*federationNode{laptop, server} {
		node.gate.proxyDown.Store(true)
		node.server.CloseClientConnections()
	}
	namedLead(t, server, forkProject, "server-fork-lead")
	linkAction(t, laptop, map[string]string{"action": "project-link", "projectId": forkProject, "survivor": upstreamProject})
	laptop.gate.proxyDown.Store(false)
	server.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the server takes the link and holds it for its fork's lead", 15*time.Second, func() bool {
		links, err := server.reg.ProjectLinks()
		if err != nil || len(links) != 1 || !links[0].Linked {
			return false
		}
		server.daemon.discoverCatalog(context.Background())
		fork := snapshotProject(t, server, forkProject)
		return fork != nil && fork.LinkHeld != ""
	})
	if entries := leadHistory(t, server, forkProject); !slices.Equal(entries, []string{"set:server-fork-lead"}) {
		t.Fatalf("fork history while held = %v, want the current lead's record kept", entries)
	}
	clearLead(t, server, forkProject)
	testkit.Eventually(t, "clearing the lead releases the link and moves the fork's history", 15*time.Second, func() bool {
		return linkHonoured(t, server) && len(leadHistory(t, server, forkProject)) == 0 && slices.Equal(leadHistory(t, server, upstreamProject), []string{"set:server-fork-lead", "cleared:server-fork-lead"})
	})
}

func TestARepositoryGainsItsRootCommitWithItsFirstCommit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "fresh")
	gitRun(t, t.TempDir(), "init", "-b", "main", repo)
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test")
	repositories := &catalogRepositoryCache{entries: map[string]catalogRepositoryEntry{}}
	resolve := func() []string {
		identity, _, transient := resolveCatalogIdentity(context.Background(), repo, nil, projectLinkSet{}, repositories)
		if transient || identity.Project == nil {
			t.Fatalf("identity=%+v transient=%v", identity, transient)
		}
		return identity.Project.RootCommits
	}
	if roots := resolve(); len(roots) != 0 {
		t.Fatalf("a repository without commits has roots %v", roots)
	}
	gitRun(t, repo, "commit", "--allow-empty", "-m", "first")
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(head))
	if roots := resolve(); len(roots) != 1 || roots[0] != root {
		t.Fatalf("after the first commit roots=%v want [%s]", roots, root)
	}
}

func TestALinkRefusesToStrandAProjectsLeadAndNamesTheWayRound(t *testing.T) {
	cases := []struct {
		name   string
		leads  []string
		boards []string
		want   string
	}{
		{"only the joined project has a lead", []string{forkProject}, nil, "fork already has a lead of its own; link the other way round: agenthail project link " + upstreamProject + " " + forkProject},
		{"both projects have a lead", []string{forkProject, upstreamProject}, nil, "fork has a lead and upstream has a lead; merging two projects' leads and ledger boards isn't supported"},
		{"both projects have a ledger board", nil, []string{forkProject, upstreamProject}, "fork has a ledger board and upstream has a ledger board; merging two projects' leads and ledger boards isn't supported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			node := newFederationNode(t)
			root := t.TempDir()
			for index, project := range c.leads {
				spec := registry.CoordinatorSpec{ProjectID: project, ProjectRoot: root, Name: fmt.Sprintf("lead-%d", index), Surface: "codex", Cwd: root}
				if _, err := node.reg.SetCoordinator(spec, registry.CoordinatorChange{ChangedBy: "test"}); err != nil {
					t.Fatal(err)
				}
			}
			for _, project := range c.boards {
				if err := ledger.NewStore(node.reg.WriteDB(), node.reg.ReadDB()).Enable(project, ledger.AttachEachPrompt); err != nil {
					t.Fatal(err)
				}
			}
			response := node.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-link", "projectId": forkProject, "survivor": upstreamProject})
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), c.want) {
				t.Fatalf("link: %d %s; want %q", response.Code, response.Body, c.want)
			}
			if links, err := node.reg.ProjectLinks(); err != nil || len(links) != 0 {
				t.Fatalf("a refused link was recorded: %+v err=%v", links, err)
			}
		})
	}
}
