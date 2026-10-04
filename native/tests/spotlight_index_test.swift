import Foundation

@main
struct SpotlightIndexTest {
    static func main() {
        let builder = session("a", alias: "builder", cwd: "/work/agenthail")
        let plain = session("b", name: "Fix tests", cwd: nil)
        let entry = SpotlightEntry(builder)
        check(entry.title == "@builder" && entry.detail == "Codex · agenthail", "an entry shows the handle, agent, and folder")
        check(SpotlightEntry(plain).detail == "Codex", "an entry without a folder shows only the agent")

        let first = SpotlightEntry.changes(from: [:], to: [builder, plain, builder])
        check(first.upserts.map(\.id) == ["a", "b"] && first.removals.isEmpty, "new sessions are indexed once each")

        let indexed = Dictionary(uniqueKeysWithValues: first.upserts.map { ($0.id, $0) })
        let unchanged = SpotlightEntry.changes(from: indexed, to: [builder, plain])
        check(unchanged.upserts.isEmpty && unchanged.removals.isEmpty, "unchanged sessions are not reindexed")

        let renamed = session("b", name: "Fix flaky tests", cwd: nil)
        let next = SpotlightEntry.changes(from: indexed, to: [renamed])
        check(next.upserts.map(\.title) == ["Fix flaky tests"] && next.removals == ["a"], "renamed sessions are reindexed and gone sessions removed")
    }

    private static func session(_ id: String, alias: String? = nil, name: String = "", cwd: String?) -> SessionState {
        SessionState(id: id, surface: "codex", name: name, alias: alias, status: "idle", lastActive: nil, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil, cwd: cwd)
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
