import Foundation

@MainActor
final class FakeStore: SpotlightStore {
    struct Failure: Error {}
    var operations: [String] = []
    var holdDelete = false
    var failNext: String?
    private var gate: CheckedContinuation<Void, Never>?

    func deleteAll() async throws {
        operations.append("deleteAll")
        if holdDelete { await withCheckedContinuation { gate = $0 } }
        try failIfNeeded("deleteAll")
    }

    func delete(_ ids: [String]) async throws {
        operations.append("delete \(ids.joined(separator: ","))")
        try failIfNeeded("delete")
    }

    func index(_ entries: [SpotlightEntry]) async throws {
        operations.append("index \(entries.map(\.id).joined(separator: ","))")
        try failIfNeeded("index")
    }

    func release() {
        holdDelete = false
        gate?.resume()
        gate = nil
    }

    private func failIfNeeded(_ operation: String) throws {
        guard failNext == operation else { return }
        failNext = nil
        throw Failure()
    }
}

@main
struct SpotlightIndexTest {
    @MainActor
    static func main() async {
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

        let store = FakeStore()
        var failures = 0
        let sync = SpotlightSync(store: store) { _ in failures += 1 }
        store.holdDelete = true
        sync.update([builder, plain])
        for _ in 0..<5 { await Task.yield() }
        check(store.operations == ["deleteAll"], "nothing is indexed until clearing earlier items finishes")
        store.release()
        await sync.settle()
        check(store.operations == ["deleteAll", "index a,b"] && sync.indexed.count == 2, "sessions are indexed after the clear")

        store.operations = []
        sync.update([builder, plain])
        await sync.settle()
        check(store.operations.isEmpty, "an unchanged catalog writes nothing")

        store.failNext = "index"
        sync.update([builder, renamed])
        await sync.settle()
        check(failures == 1 && sync.indexed["b"]?.title == "Fix tests", "a failed write leaves the confirmed state unchanged")
        sync.update([builder, renamed])
        await sync.settle()
        check(sync.indexed["b"]?.title == "Fix flaky tests", "the next update retries a failed write")

        store.operations = []
        store.failNext = "deleteAll"
        sync.clear()
        await sync.settle()
        check(failures == 2 && !sync.indexed.isEmpty, "a failed clear is not recorded as done")
        sync.clear()
        await sync.settle()
        check(sync.indexed.isEmpty && store.operations == ["deleteAll", "deleteAll"], "turning Spotlight off retries until it clears")
        sync.clear()
        await sync.settle()
        check(store.operations.count == 2, "turning it off again writes nothing")

        sync.update([builder])
        await sync.settle()
        check(store.operations.last == "index a", "turning it back on indexes the catalog again")
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
