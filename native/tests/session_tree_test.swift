import Foundation

@main
struct SessionTreeTest {
    static func main() {
        let now = SessionTree.parseTimestamp("2026-10-04T12:00:00Z")!
        let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)
        func session(_ id: String, status: String, lastActive: String, project: String, checkout: String, branch: String?, detached: String? = nil) -> SessionState {
            SessionState(id: id, surface: "codex", name: id, alias: nil, status: status, lastActive: lastActive, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo/\(checkout)", hostProject: HostProjectIdentity(id: project, displayName: project, commonDir: "/repo/.git", path: nil), checkout: CheckoutIdentity(id: checkout, path: "/repo/\(checkout)", branch: branch, detachedHead: detached, isMain: checkout == "main", dirty: false))
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

        let running = SessionTree.build(sessions, filter: .running, attentionSessionIDs: [], now: now)
        expect(running.projects.flatMap { $0.checkouts.flatMap(\.sessions) }.map(\.id) == ["busy"], "Running shows only working sessions")

        let all = SessionTree.build(sessions, filter: .all, attentionSessionIDs: [], now: now)
        expect(all.projects[0].checkouts.first { $0.label == "main" }?.sessions.map(\.id) == ["idle", "old"], "All keeps older sessions, newest first")
        print("session tree tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
