import Foundation

@main
struct SessionTreeTest {
    static func main() {
        let now = SessionTree.parseTimestamp("2026-10-04T12:00:00Z")!
        expect(SessionTree.parseTimestamp("2026-10-04 12:00:00") == now, "SQLite UTC timestamps parse as UTC")
        let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)
        func session(_ id: String, status: String, lastActive: String, project: String, checkout: String, branch: String?, detached: String? = nil, name: String? = nil) -> SessionState {
            SessionState(id: id, surface: "codex", name: name ?? id, alias: nil, status: status, lastActive: lastActive, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo/\(checkout)", hostProject: HostProjectIdentity(id: project, displayName: project, commonDir: "/repo/.git", path: nil), checkout: CheckoutIdentity(id: checkout, path: "/repo/\(checkout)", branch: branch, detachedHead: detached, isMain: checkout == "main", dirty: false))
        }
        let sessions = [
            session("old", status: "idle", lastActive: "2026-10-01T12:00:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("busy", status: "busy", lastActive: "2026-10-04T11:59:00.250-04:00", project: "agenthail", checkout: "feat", branch: "feat/x"),
            session("idle", status: "idle", lastActive: "2026-10-04T11:00:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("other", status: "idle", lastActive: "2026-10-04T10:00:00Z", project: "fable", checkout: "wt", branch: nil, detached: "de2fda1f716")
        ]

        let recent = SessionTree.build(sessions, filter: .recent, attentionSessionIDs: ["other"], now: now)
        expect(recent.counts == [.running: 1, .recent: 3, .all: 4], "counts per filter: \(recent.counts)")
        expect(recent.projects.map(\.name) == ["agenthail", "fable"], "projects ordered by latest activity")
        expect(recent.projects[0].checkouts.map(\.label) == ["feat/x", "main"], "checkouts ordered by latest activity")
        expect(recent.projects[0].checkouts[1].sessions.map(\.id) == ["idle"], "sessions older than a day leave Recent")
        expect(recent.projects[1].checkouts[0].label == "detached at de2fda1", "detached checkout label")
        expect(recent.needsYou.map(\.id) == ["other"], "attention sessions appear under Needs you")
        expect(SessionTree.needsYou(sessions, attentionSessionIDs: ["other", "gone"]).map(\.id) == ["other"], "attention for a session missing from the catalog is not counted")

        let running = SessionTree.build(sessions, filter: .running, attentionSessionIDs: [], now: now)
        expect(running.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id) == ["busy"], "Running shows only working sessions")

        let all = SessionTree.build(sessions, filter: .all, attentionSessionIDs: [], now: now)
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

        let familyTree = SessionTree.build(family + [claudeLead], filter: .running, attentionSessionIDs: [], now: now)
        expect(families[1].isCurrent == false && families[0].isCurrent, "a family is current while any member works")
        expect(familyTree.counts[.running] == 2, "Running counts working families, not subagent threads: \(familyTree.counts)")
        expect(familyTree.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id).sorted() == ["claude-lead", "lead"], "a family with a working subagent is running and shows its root")
        let allFamilies = SessionTree.build(family, filter: .all, attentionSessionIDs: [], now: now)
        expect(allFamilies.counts[.all] == 2, "All counts families: \(allFamilies.counts)")
        let capFamily = allFamilies.projects[0].limited(to: 1, keeping: "euclid")
        expect(capFamily.checkouts.flatMap(\.families).map(\.id) == ["lead"], "selecting a subagent keeps its family visible past the cap")

        let mixed = [
            session("whole", status: "idle", lastActive: "2026-10-04T15:58:00Z", project: "agenthail", checkout: "main", branch: "main"),
            session("short-fraction", status: "idle", lastActive: "2026-10-04T11:59:58.03-04:00", project: "agenthail", checkout: "main", branch: "main"),
            session("sqlite", status: "idle", lastActive: "2026-10-04 15:59:00", project: "agenthail", checkout: "main", branch: "main"),
            session("unparsed", status: "idle", lastActive: "yesterday", project: "agenthail", checkout: "main", branch: "main")
        ]
        expect(SessionTree.newestFirst(mixed).map(\.id) == ["short-fraction", "sqlite", "whole", "unparsed"], "newest first across timestamp formats, unparsed last")
        let sharedRow = #"{"id":"remote-b","surface":"claude","name":"Fixture","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}"#
        let sharedJSON = sharedRow + #","sharedWith":[{"id":"remote-a","name":"Fixture","pid":4101,"status":"busy","startedAt":"2026-10-04T11:40:00.250-04:00"}]}"#
        let shared = try! JSONDecoder().decode(SessionState.self, from: Data(sharedJSON.utf8))
        expect(SharedConversation.badge(shared) == "⧉2", "a row shared with one other process shows a two-process badge")
        expect(SharedConversation.badgeLabel(shared) == "Open in 2 processes", "the badge has a spoken label")
        let note = SharedConversation.note(shared) ?? ""
        expect(note.hasPrefix("Also open in pid 4101 (started ") && note.hasSuffix("). Messages here go to this process."), "the header names the other pid and its start: \(note)")
        expect(SharedConversation.openLabel(shared.sharedWith![0]) == "Open other process, pid 4101", "the open action names the pid without digit grouping")
        var three = shared
        three.sharedWith?.append(SharedProcess(id: "remote-c", pid: 4103, status: "idle"))
        expect(SharedConversation.badge(three) == "⧉3", "the badge counts every process")
        expect(SharedConversation.note(three) == "Also open in 2 other processes. Messages here go to this process.", "several peers are summarized and listed")
        expect(SharedConversation.peerLabel(three.sharedWith![1]) == "pid 4103 (start time unknown)", "a peer without a start time says so")
        let unshared = try! JSONDecoder().decode(SessionState.self, from: Data((sharedRow + "}").utf8))
        expect(unshared.sharedWith == nil && SharedConversation.badge(unshared) == nil && SharedConversation.note(unshared) == nil, "an unshared row has no badge or note")
        print("session tree tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
