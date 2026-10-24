@testable import AgenthailHelpers
import Foundation

@MainActor
final class FakeStore: SpotlightStore {
    struct Failure: Error {}
    var operations: [String] = []
    var hold: String?
    var failNext: String?
    private var gate: CheckedContinuation<Void, Never>?

    func deleteAll() async throws {
        operations.append("deleteAll")
        try await pause("deleteAll")
    }

    func delete(_ ids: [String]) async throws {
        operations.append("delete \(ids.joined(separator: ","))")
        try await pause("delete")
    }

    func index(_ entries: [SpotlightEntry]) async throws {
        operations.append("index \(entries.map(\.id).joined(separator: ","))")
        try await pause("index")
    }

    func release() {
        hold = nil
        gate?.resume()
        gate = nil
    }

    private func pause(_ operation: String) async throws {
        if hold == operation { await withCheckedContinuation { gate = $0 } }
        try failIfNeeded(operation)
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
        let folders = ["/work/agenthail/": "agenthail", "/": "/", "": "/", "//": "/", "/a//b": "b", ".": "/", "relative/dir": "dir", "/a b/c d": "c d"]
        for (path, name) in folders { check(SpotlightEntry.folderName(path) == name, "folder of \(path.debugDescription) is \(SpotlightEntry.folderName(path).debugDescription), not \(name.debugDescription)") }
        check(SpotlightEntry(session("c", cwd: "/work/agenthail/")).detail == "Codex · agenthail", "a trailing slash still names the folder")
        check(SpotlightEntry(session("d", cwd: "/")).detail == "Codex · /", "the root folder shows as a slash")
        check(SpotlightEntry(session("e", cwd: "")).detail == "Codex · /", "an empty folder resolves to the root, as it does in the app")
        let missing = "/nonexistent-\(UUID().uuidString)/agenthail"
        check(SpotlightEntry.folderName(missing) == "agenthail", "a folder that does not exist is still named")
        FileManager.default.changeCurrentDirectoryPath(NSTemporaryDirectory())
        check(SpotlightEntry.folderName("") == "/" && SpotlightEntry.folderName(".") == "/", "the working directory does not change a folder name")

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
        let sync = SpotlightSync(store: store, retryDelay: .milliseconds(20), failed: { _ in failures += 1 })
        store.hold = "deleteAll"
        sync.update([builder, plain])
        await until { store.operations == ["deleteAll"] }
        check(store.operations == ["deleteAll"], "nothing is indexed until clearing earlier items finishes")
        store.release()
        await sync.settle()
        check(store.operations == ["deleteAll", "index a,b"] && sync.indexed.count == 2, "sessions are indexed after the clear")

        store.operations = []
        sync.update([builder, plain])
        await sync.settle()
        check(store.operations.isEmpty, "an unchanged catalog writes nothing")

        store.failNext = "index"
        store.hold = "index"
        sync.update([builder, renamed])
        await until { !store.operations.isEmpty }
        store.release()
        store.hold = "index"
        await until { store.operations.count == 2 }
        check(failures == 1 && sync.indexed["b"]?.title == "Fix tests", "a failed write leaves the confirmed state unchanged")
        store.release()
        await sync.settle()
        check(sync.indexed["b"]?.title == "Fix flaky tests", "a failed write is retried without another update")

        store.operations = []
        store.failNext = "delete"
        store.hold = "delete"
        sync.update([renamed])
        await until { store.operations == ["delete a"] }
        sync.update([builder, renamed, session("c", name: "Docs", cwd: nil)])
        store.release()
        await sync.settle()
        check(failures == 2 && sync.indexed.keys.sorted() == ["a", "b", "c"] && store.operations == ["delete a", "index c"], "a failure moves on to the newest catalog")

        let slow = FakeStore()
        let patient = SpotlightSync(store: slow, retryDelay: .seconds(60))
        slow.failNext = "deleteAll"
        patient.update([builder])
        await until { slow.operations == ["deleteAll"] }
        patient.clear()
        await patient.settle()
        check(slow.operations == ["deleteAll", "deleteAll"] && patient.indexed.isEmpty, "a new target does not wait out the retry delay")

        let unsupported = FakeStore()
        let stopped = SpotlightSync(store: unsupported, retryDelay: .milliseconds(1), permanent: { $0 is FakeStore.Failure })
        unsupported.failNext = "deleteAll"
        stopped.update([builder])
        await stopped.settle()
        check(unsupported.operations == ["deleteAll"], "a permanent failure is not retried")
        stopped.update([builder])
        await stopped.settle()
        check(unsupported.operations == ["deleteAll", "deleteAll", "index a"], "a new catalog tries again after a permanent failure")

        store.operations = []
        store.failNext = "deleteAll"
        sync.clear()
        await sync.settle()
        check(failures == 3 && sync.indexed.isEmpty && store.operations == ["deleteAll", "deleteAll"], "turning Spotlight off retries until it clears")
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

    /// Waits for the sync to reach a state, so a check does not depend on how
    /// many scheduler turns it took to get there. It runs on the main actor,
    /// where the sync and the store it reads live.
    @MainActor
    private static func until(_ condition: () -> Bool) async {
        let deadline = Date().addingTimeInterval(5)
        while !condition(), Date() < deadline { await Task.yield() }
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
