import Foundation

@main
struct SessionTreeTest {
    static func main() {
        let now = SessionTree.parseTimestamp("2026-10-04T12:00:00Z")!
        expect(SessionTree.parseTimestamp("2026-10-04 12:00:00") == now, "SQLite UTC timestamps parse as UTC")
        let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)
        func session(_ id: String, status: String, lastActive: String, project: String, checkout: String, branch: String?, detached: String? = nil, name: String? = nil) -> SessionState {
            SessionState(id: id, surface: "codex", name: name ?? id, alias: nil, status: status, lastActive: lastActive, queueCount: 0, open: true, current: status == "busy" || now.timeIntervalSince(SessionTree.parseTimestamp(lastActive) ?? .distantPast) <= 24 * 60 * 60, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo/\(checkout)", project: ProjectIdentity(id: project, displayName: project, rootPath: "/repo", kind: "git"), checkout: CheckoutIdentity(id: checkout, path: "/repo/\(checkout)", branch: branch, detachedHead: detached, isMain: checkout == "main", dirty: false))
        }
        let sessions = [
            session("old", status: "idle", lastActive: "2026-10-01T12:00:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("busy", status: "busy", lastActive: "2026-10-04T11:59:00.250-04:00", project: "agenthail", checkout: "feat", branch: "feat/x"),
            session("idle", status: "idle", lastActive: "2026-10-04T11:00:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("other", status: "idle", lastActive: "2026-10-04T10:00:00Z", project: "fable", checkout: "wt", branch: nil, detached: "de2fda1f716")
        ]

        let recent = SessionTree.build(sessions, filter: .recent, attentionSessionIDs: ["other"])
        expect(recent.counts == [.running: 1, .recent: 3, .all: 4, .archived: 0], "counts per filter: \(recent.counts)")
        expect(recent.projects.map(\.name) == ["agenthail", "fable"], "projects ordered by latest activity")
        expect(recent.projects[0].checkouts.map(\.label) == ["feat/x", "main"], "checkouts ordered by latest activity")
        expect(recent.projects[0].checkouts[1].sessions.map(\.id) == ["idle"], "sessions the daemon does not mark current leave Recent")
        expect(recent.projects[1].checkouts[0].label == "detached at de2fda1", "detached checkout label")
        expect(recent.needsYou.map(\.id) == ["other"], "attention sessions appear under Needs you")
        expect(SessionTree.needsYou(sessions, attentionSessionIDs: ["other", "gone"]).map(\.id) == ["other"], "attention for a session missing from the catalog is not counted")

        let running = SessionTree.build(sessions, filter: .running, attentionSessionIDs: [])
        expect(running.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id) == ["busy"], "Running shows only working sessions")

        let all = SessionTree.build(sessions, filter: .all, attentionSessionIDs: [])
        expect(all.projects[0].checkouts.first { $0.label == "main" }?.sessions.map(\.id) == ["idle", "old"], "All keeps older sessions, newest first")
        let project = all.projects[0]
        let capped = project.limited(to: 2, keeping: nil)
        expect(capped.sessionCount == 2 && capped.checkouts.flatMap(\.sessions).map(\.id) == ["busy", "idle"], "a capped workspace keeps its most recent sessions")
        expect(capped.checkouts.map(\.label) == ["feat/x", "main"], "checkouts with no visible sessions are hidden")
        let keepSelected = project.limited(to: 1, keeping: "old")
        expect(Set(keepSelected.checkouts.flatMap(\.sessions).map(\.id)) == ["busy", "old"], "the selected session stays visible past the cap")
        func member(_ id: String, parent: String, root: String, depth: Int, nickname: String, role: String? = nil, status: String, lastActive: String) -> SessionState {
            var state = session(id, status: status, lastActive: lastActive, project: "agenthail", checkout: "main", branch: "main")
            state.subagent = SubagentIdentity(parentId: parent, rootId: root, depth: depth, nickname: nickname, role: role)
            state.current = status == "busy"
            return state
        }
        let lead = session("lead", status: "idle", lastActive: "2026-10-04T09:00:00Z", project: "agenthail", checkout: "main", branch: "main", name: "Build the parser")
        let family = [
            lead,
            member("ada", parent: "lead", root: "lead", depth: 1, nickname: "Ada", status: "busy", lastActive: "2026-10-04T11:58:00Z"),
            member("euclid", parent: "ada", root: "lead", depth: 2, nickname: "Euclid", role: "reviewer", status: "busy", lastActive: "2026-10-04T11:57:00Z"),
            member("bo", parent: "lead", root: "lead", depth: 1, nickname: "Bo", status: "idle", lastActive: "2026-10-04T08:00:00Z"),
            member("orphan", parent: "gone", root: "gone", depth: 1, nickname: "Dee", status: "idle", lastActive: "2026-10-04T07:00:00Z")
        ]
        let families = SessionFamilies.build(family)
        expect(families.map(\.id) == ["lead", "orphan"], "subagents nest under a listed parent; an unlisted parent leaves a root: \(families.map(\.id))")
        expect(families[0].members.map(\.id) == ["ada", "euclid", "bo"] && families[0].members.map(\.depth) == [1, 2, 1], "members follow their parent depth-first")
        expect(families[0].subagentCount == 3 && families[0].workingSubagents == 2 && families[0].isWorking, "the root rolls up every descendant and working count")
        expect(SessionFamilies.title(family[2], in: family) == "Build the parser > Ada > Euclid (reviewer)", "a standalone subagent reads as its parent chain: \(SessionFamilies.title(family[2], in: family))")
        expect(SessionFamilies.title(family[4], in: family) == "Dee" && SessionFamilies.label(family[1]) == "Ada", "subagent labels use the nickname")

        var claudeLead = session("claude-lead", status: "idle", lastActive: "2026-10-04T11:00:00Z", project: "fable", checkout: "wt", branch: "main")
        claudeLead.subagents = SubagentRollup(count: 4, working: 1)
        let claude = SessionFamilies.build([claudeLead])[0]
        expect(claude.subagentCount == 4 && claude.workingSubagents == 1 && claude.isWorking, "observed Claude subagents roll into the parent row")

        let familyTree = SessionTree.build(family + [claudeLead], filter: .running, attentionSessionIDs: [])
        expect(families[1].isCurrent == false && families[0].isCurrent, "a family is current while any member works")
        expect(familyTree.counts[.running] == 2, "Running counts working families, not subagent threads: \(familyTree.counts)")
        expect(familyTree.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id).sorted() == ["claude-lead", "lead"], "a family with a working subagent is running and shows its root")
        let allFamilies = SessionTree.build(family, filter: .all, attentionSessionIDs: [])
        expect(allFamilies.counts[.all] == 2, "All counts families: \(allFamilies.counts)")
        let capFamily = allFamilies.projects[0].limited(to: 1, keeping: "euclid")
        expect(capFamily.checkouts.flatMap(\.families).map(\.id) == ["lead"], "selecting a subagent keeps its family visible past the cap")

        var offline = session("gone", status: "offline", lastActive: "2026-10-04T11:30:00Z", project: "agenthail", checkout: "main", branch: "main")
        offline = SessionState(id: offline.id, surface: "claude", name: "gone", alias: nil, status: "offline", lastActive: offline.lastActive, queueCount: 0, open: false, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: offline.cwd, project: offline.project, checkout: offline.checkout)
        let notLoaded = session("cold", status: "notLoaded", lastActive: "2026-10-04T11:40:00Z", project: "agenthail", checkout: "main", branch: "main")
        let unknown = session("lost", status: "unknown", lastActive: "2026-10-04T11:41:00Z", project: "agenthail", checkout: "main", branch: "main")
        let refined = sessions + [offline, notLoaded, unknown]
        func ids(_ tree: SessionTree) -> [String] { tree.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id).sorted() }
        let idleOnly = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(status: .idle), attentionSessionIDs: ["busy"])
        expect(ids(idleOnly) == ["idle", "old", "other"], "the idle filter keeps idle sessions: \(ids(idleOnly))")
        expect(idleOnly.counts == [.running: 0, .recent: 2, .all: 3, .archived: 0], "scope counts follow the refinement: \(idleOnly.counts)")
        expect(idleOnly.needsYou.map(\.id) == ["busy"], "Needs you is not narrowed by the refinement")
        let unavailable = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(status: .unavailable), attentionSessionIDs: [])
        expect(ids(unavailable) == ["cold", "lost"], "unavailable covers unknown and not loaded: \(ids(unavailable))")
        let claudeOnly = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(surface: "claude"), attentionSessionIDs: [])
        expect(ids(claudeOnly) == ["gone"], "the surface filter keeps one surface: \(ids(claudeOnly))")
        let offlineCodex = SessionTree.build(refined, filter: .all, refinement: SessionRefinement(status: .offline, surface: "codex"), attentionSessionIDs: [])
        expect(offlineCodex.projects.isEmpty, "status and surface combine")
        let busyRunning = SessionTree.build(refined, filter: .running, refinement: SessionRefinement(status: .busy), attentionSessionIDs: [])
        expect(ids(busyRunning) == ["busy"], "Running with Working shows the working session")
        expect(SessionTree.build(refined, filter: .all, refinement: SessionRefinement(), attentionSessionIDs: []).counts[.all] == 7, "no refinement keeps every family")
        let workingMember = SessionTree.build(family, filter: .all, refinement: SessionRefinement(status: .busy), attentionSessionIDs: [])
        expect(workingMember.projects.flatMap { $0.checkouts.flatMap(\.families) }.map(\.id) == ["lead"], "a family matches when any member matches")
        expect(SessionRefinement.surfaces(refined, configured: [SurfaceState(name: "notion", connected: false, error: nil, health: "unavailable", healthDetail: nil, capabilities: caps)]) == ["claude", "codex", "notion"], "surface choices merge configured surfaces and sessions")
        expect(SessionRefinement.surfaceLabel("codex") == "Codex", "surface labels are capitalized")

        var hidden = session("hidden", status: "idle", lastActive: "2026-10-04T11:50:00Z", project: "agenthail", checkout: "main", branch: "main")
        hidden.archived = true
        var loose = session("loose", status: "idle", lastActive: "2026-10-04T11:55:00Z", project: "agenthail", checkout: "main", branch: "main")
        loose.project = nil
        loose.cwd = "/tmp/scratch"
        let archiveTree = SessionTree.build(sessions + [hidden, loose], filter: .all, attentionSessionIDs: [])
        expect(archiveTree.counts == [.running: 1, .recent: 4, .all: 5, .archived: 1], "archived sessions leave every scope but Archived: \(archiveTree.counts)")
        expect(!ids(archiveTree).contains("hidden"), "All hides archived sessions")
        expect(archiveTree.projects.last?.identity == nil && archiveTree.projects.last?.name == "No project", "sessions without a project group last")
        expect(ids(SessionTree.build(sessions + [hidden], filter: .archived, attentionSessionIDs: [])) == ["hidden"], "Archived lists archived sessions")

        var remote = session("h1::remote", status: "idle", lastActive: "2026-10-04T11:58:00Z", project: "h1::agenthail", checkout: "wt", branch: "history-truth")
        remote.host = SessionHost(id: "h1", name: "forge-agents.tail0000.ts.net", state: "connected")
        var remoteLoose = SessionState(id: "h1::loose", surface: "codex", name: "remote loose", alias: nil, status: "idle", lastActive: "2026-10-04T11:57:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/home/demo")
        remoteLoose.host = remote.host
        let hosted = SessionTree.build(sessions + [remote, loose, remoteLoose], filter: .all, attentionSessionIDs: [])
        expect(hosted.projects.first { $0.id == "h1::agenthail" }?.host == "forge-agents", "a remote project names its host without the domain")
        expect(hosted.projects.first { $0.id == "agenthail" }?.host == nil, "a project on this host carries no host")
        expect(Set(hosted.projects.suffix(2).map(\.host)) == [nil, "forge-agents"] && hosted.projects.suffix(2).allSatisfy { $0.identity == nil }, "sessions without a project group per host, last: \(hosted.projects.map(\.id))")

        var queued = session("queued", status: "idle", lastActive: "2026-09-01T00:00:00Z", project: "agenthail", checkout: "main", branch: "main")
        queued.queueCount = 1
        let flagged = session("flagged", status: "idle", lastActive: "2026-09-02T00:00:00Z", project: "agenthail", checkout: "main", branch: "main")
        var rolled = session("rolled", status: "idle", lastActive: "2026-09-03T00:00:00Z", project: "agenthail", checkout: "main", branch: "main")
        rolled.subagents = SubagentRollup(count: 2, working: 1)
        let crowded = sessions + [queued, flagged, rolled] + (0..<6).map { session("filler\($0)", status: "idle", lastActive: "2026-10-04T11:3\($0):00Z", project: "agenthail", checkout: "main", branch: "main") }
        let collapsed = SessionTree.build(crowded, filter: .all, attentionSessionIDs: ["flagged"]).projects[0].limited(to: SessionTree.collapsedSessionLimit, keeping: nil)
        let shownIDs = Set(collapsed.checkouts.flatMap(\.sessions).map(\.id))
        expect(shownIDs.isSuperset(of: ["busy", "queued", "flagged", "rolled"]) && !shownIDs.contains("old"), "collapsing a project keeps working, working-subagent, queued and attention sessions: \(shownIDs.sorted())")

        let mixed = [
            session("whole", status: "idle", lastActive: "2026-10-04T15:58:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("short-fraction", status: "idle", lastActive: "2026-10-04T11:59:58.03-04:00", project: "agenthail", checkout: "main", branch: "main"),
            session("sqlite", status: "idle", lastActive: "2026-10-04 15:59:00", project: "agenthail", checkout: "main", branch: "main"),
            session("unparsed", status: "idle", lastActive: "yesterday", project: "agenthail", checkout: "main", branch: "main")
        ]
        expect(SessionTree.newestFirst(mixed).map(\.id) == ["short-fraction", "sqlite", "whole", "unparsed"], "newest first across timestamp formats, unparsed last")
        print("session tree tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
