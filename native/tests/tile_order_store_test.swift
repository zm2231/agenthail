@testable import AgenthailHelpers
import Foundation

struct TileArrangementVectors: Decodable {
    struct Project: Decodable { let id: String; var archived: Bool? = nil }
    struct Change: Decodable { let kind: String; let id: String; var target: String? = nil; var by: Int? = nil; var on: Bool? = nil }
    struct After: Decodable { let order: [String]; let pinned: [String] }
    struct Case: Decodable {
        let name: String
        let projects: [Project]
        let limit: Int
        let order: [String]
        let pinned: [String]
        let shown: [String]
        let more: [String]
        var change: Change? = nil
        var after: After? = nil
    }
    let cases: [Case]
}

actor FakeDaemon {
    var state = TileOrderState(order: [], pinned: [], revision: 0)
    var offline = false
    var held = false
    var lose = false
    var writes: [TileOrderState] = []

    func put(_ sent: TileOrderState) async throws -> TileWrite {
        while held { try await Task.sleep(nanoseconds: 2_000_000) }
        if offline { throw URLError(.notConnectedToInternet) }
        writes.append(sent)
        guard sent.revision == state.revision else { return .stale(state) }
        state = TileOrderState(order: sent.order, pinned: sent.pinned, revision: state.revision + 1)
        if lose {
            lose = false
            throw URLError(.networkConnectionLost)
        }
        return .saved(state)
    }

    func set(_ next: TileOrderState) { state = next }
    func go(offline: Bool) { self.offline = offline }
    func hold(_ held: Bool) { self.held = held }
    func loseNextAnswer() { lose = true }
}

@main
struct TileOrderStoreTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/tile-arrangement-vectors.json")

    @MainActor
    static func main() async throws {
        let vectors = try JSONDecoder().decode(TileArrangementVectors.self, from: Data(contentsOf: fixture))
        for vector in vectors.cases {
            let projects = vector.projects.map { ProjectState(id: $0.id, displayName: $0.id, archived: $0.archived) }
            let tiles = ProjectTiles.arranged(projects, limit: vector.limit, order: vector.order, pinned: Set(vector.pinned))
            check(tiles.shown.map(\.id) == vector.shown && tiles.more.map(\.id) == vector.more, "\(vector.name): \(tiles.shown.map(\.id)) \(tiles.more.map(\.id))")
            guard let change = vector.change, let after = vector.after else { continue }
            let made: TileChange = switch change.kind {
            case "pin": .pin(change.id, change.on ?? true)
            case "move": .move(change.id, before: change.target ?? "")
            default: .step(change.id, by: change.by ?? 0)
            }
            let next = TileArrangement(order: vector.order, pinned: Set(vector.pinned)).applying(made, arranged: (tiles.shown + tiles.more).map(\.id))
            check(next.order == after.order && next.pinned.sorted() == after.pinned, "\(vector.name): after \(next.order) \(next.pinned.sorted())")
        }

        let projects = ["a", "b", "c"].map { ProjectState(id: $0, displayName: $0) }

        try await withStore { defaults, suite in
            defaults.set(["c", "a", "b"], forKey: "projectTileOrder")
            defaults.set(["b"], forKey: "projectTilePins")
            let daemon = FakeDaemon()
            let store = TileOrderStore(defaults: defaults, key: suite, legacy: (order: "projectTileOrder", pinned: "projectTilePins"))
            check(store.arrangement == TileArrangement(order: ["c", "a", "b"], pinned: ["b"]), "the Mac starts from the order it kept before the daemon held one")
            store.seed(["a", "b", "c"])
            store.adopt(TileOrderState(order: [], pinned: [], revision: 0)) { try await daemon.put($0) }
            let seeded = try await settle { await daemon.state.revision == 1 }
            check(seeded, "a daemon with no order gets the Mac's")
            let first = await daemon.state
            check(first == TileOrderState(order: ["c", "a", "b"], pinned: ["b"], revision: 1), "the Mac's own order seeds it, not the ranking: \(first)")
            store.adopt(await daemon.state) { try await daemon.put($0) }
            let writes = await daemon.writes.count
            check(writes == 1, "the order is sent once")
        }

        try await withStore { defaults, suite in
            let daemon = FakeDaemon()
            await daemon.set(TileOrderState(order: ["b", "a", "c"], pinned: [], revision: 4))
            let store = TileOrderStore(defaults: defaults, key: suite)
            store.seed(["a", "b", "c"])
            store.adopt(await daemon.state) { try await daemon.put($0) }
            check(store.arrangement.order == ["b", "a", "c"], "a device with only the ranking takes the daemon's order: \(store.arrangement.order)")
            let none = await daemon.writes.isEmpty
            check(none, "and writes nothing")

            _ = store.tiles(projects, limit: 3)
            await daemon.set(TileOrderState(order: ["c", "b", "a"], pinned: [], revision: 5))
            store.change(.move("a", before: "b"), arranged: ["b", "a", "c"])
            let replayed = try await settle { await daemon.state.revision == 6 }
            check(replayed, "a move against an older order is sent again")
            let moved = await daemon.state.order
            check(moved == ["c", "a", "b"], "the move is made again on the order that replaced it: \(moved)")
            check(store.arrangement.order == ["c", "a", "b"], "and shown: \(store.arrangement.order)")
            let before = await daemon.writes.count
            store.change(.step("c", by: -1), arranged: ["c", "a", "b"])
            _ = try await settle { false }
            let after = await daemon.writes.count
            check(after == before, "a move that changes nothing writes nothing")
        }

        try await withStore { defaults, suite in
            let daemon = FakeDaemon()
            await daemon.set(TileOrderState(order: ["a", "b", "c"], pinned: [], revision: 2))
            await daemon.go(offline: true)
            let store = TileOrderStore(defaults: defaults, key: suite)
            store.adopt(await daemon.state) { try await daemon.put($0) }
            store.change(.pin("c", true), arranged: ["a", "b", "c"])
            _ = try await settle { false }
            check(store.arrangement.pinned == ["c"], "a change made offline shows at once")
            let reopened = TileOrderStore(defaults: defaults, key: suite)
            check(reopened.arrangement.pinned == ["c"], "and is kept across launches")
            await daemon.set(TileOrderState(order: ["b", "a", "c"], pinned: [], revision: 3))
            await daemon.go(offline: false)
            reopened.adopt(await daemon.state) { try await daemon.put($0) }
            let sent = try await settle { await daemon.state.revision == 4 }
            check(sent, "it is sent when the daemon answers again")
            let won = await daemon.state
            check(won.pinned == ["c"] && reopened.arrangement.pinned == ["c"], "the later write wins: \(won)")
        }

        try await withStore { defaults, suite in
            let daemon = FakeDaemon()
            await daemon.set(TileOrderState(order: ["implicit:/work", "repo:github.com/example/app", "other::implicit:/srv"], pinned: ["implicit:/work"], revision: 1))
            let host = "h0123456789ab"
            let ids = TileOrderStore.IDs(inward: { ProjectTiles.colourID($0, owner: host) }, outward: { id in id.hasPrefix(host + "::") ? String(id.dropFirst(host.count + 2)) : id })
            let store = TileOrderStore(defaults: defaults, key: suite)
            store.adopt(await daemon.state, ids: ids) { try await daemon.put($0) }
            check(store.arrangement == TileArrangement(order: [host + "::implicit:/work", "repo:github.com/example/app", "other::implicit:/srv"], pinned: [host + "::implicit:/work"]),
                  "the phone names its first computer's folders as it names them everywhere: \(store.arrangement)")
            store.change(.step("other::implicit:/srv", by: -1), arranged: store.arrangement.order)
            let sent = try await settle { await daemon.state.revision == 2 }
            let named = await daemon.state
            check(sent && named.order == ["implicit:/work", "other::implicit:/srv", "repo:github.com/example/app"], "and sends them back in the computer's own names: \(named)")
        }

        try await withStore { defaults, suite in
            defaults.set(["c", "a", "b"], forKey: "projectTileOrder")
            let daemon = FakeDaemon()
            await daemon.set(TileOrderState(order: ["b", "c", "a"], pinned: ["a"], revision: 1))
            let store = TileOrderStore(defaults: defaults, key: suite, legacy: (order: "projectTileOrder", pinned: "projectTilePins"))
            store.adopt(TileOrderState(order: [], pinned: [], revision: 0)) { try await daemon.put($0) }
            _ = try await settle { await daemon.writes.count == 1 }
            _ = try await settle { store.arrangement.order == ["b", "c", "a"] }
            let after = await daemon.state
            let writes = await daemon.writes.count
            check(after.revision == 1 && writes == 1, "a seed that lost to another app's writes nothing more: \(after) after \(writes) writes")
            check(store.arrangement == TileArrangement(order: ["b", "c", "a"], pinned: ["a"]), "and takes the order that won: \(store.arrangement)")
        }

        try await withStore { defaults, suite in
            let daemon = FakeDaemon()
            await daemon.set(TileOrderState(order: ["a", "b", "c"], pinned: [], revision: 5))
            let store = TileOrderStore(defaults: defaults, key: suite)
            _ = store.tiles(projects, limit: 3)
            store.adopt(await daemon.state) { try await daemon.put($0) }
            await daemon.hold(true)
            store.change(.pin("c", true), arranged: ["a", "b", "c"])
            store.change(.move("b", before: "a"), arranged: ["c", "a", "b"])
            check(store.arrangement == TileArrangement(order: ["c", "b", "a"], pinned: ["c"]), "both edits show at once: \(store.arrangement)")
            await daemon.set(TileOrderState(order: ["a", "c", "b"], pinned: [], revision: 6))
            await daemon.hold(false)
            let landed = try await settle { await daemon.state.revision == 7 }
            let final = await daemon.state
            check(landed && final.order == ["c", "b", "a"] && final.pinned == ["c"], "both edits are made again on the order another app wrote meanwhile: \(final)")
            check(store.arrangement == TileArrangement(order: ["c", "b", "a"], pinned: ["c"]), "and shown: \(store.arrangement)")
        }

        try await withStore { defaults, suite in
            let daemon = FakeDaemon()
            await daemon.set(TileOrderState(order: ["a", "b", "c"], pinned: [], revision: 2))
            await daemon.go(offline: true)
            let first = TileOrderStore(defaults: defaults, key: suite)
            first.adopt(await daemon.state) { try await daemon.put($0) }
            first.change(.pin("a", true), arranged: ["a", "b", "c"])
            _ = try await settle { false }
            let reopened = TileOrderStore(defaults: defaults, key: suite)
            _ = reopened.tiles(projects, limit: 3)
            await daemon.go(offline: false)
            await daemon.hold(true)
            await daemon.set(TileOrderState(order: ["a", "b", "c"], pinned: [], revision: 3))
            reopened.adopt(await daemon.state) { try await daemon.put($0) }
            reopened.change(.pin("b", true), arranged: ["a", "b", "c"])
            await daemon.set(TileOrderState(order: ["c", "b", "a"], pinned: [], revision: 4))
            await daemon.hold(false)
            let landed = try await settle { await daemon.state.revision == 5 }
            let final = await daemon.state
            check(landed && final.pinned == ["a", "b"], "a pin made offline and one made after reopening both survive a refused write: \(final)")
            check(reopened.arrangement.pinned == ["a", "b"], "and both show: \(reopened.arrangement)")
        }

        try await withStore { defaults, suite in
            let daemon = FakeDaemon()
            await daemon.set(TileOrderState(order: ["a", "b", "c"], pinned: [], revision: 1))
            let store = TileOrderStore(defaults: defaults, key: suite)
            _ = store.tiles(projects, limit: 3)
            store.adopt(await daemon.state) { try await daemon.put($0) }
            await daemon.loseNextAnswer()
            store.change(.pin("a", true), arranged: ["a", "b", "c"])
            _ = try await settle { await daemon.state.revision == 2 }
            _ = try await settle { false }
            await daemon.hold(true)
            store.adopt(await daemon.state) { try await daemon.put($0) }
            store.change(.step("c", by: -1), arranged: ["a", "b", "c"])
            let advanced = await daemon.state
            await daemon.set(TileOrderState(order: ["b", "a", "c"], pinned: ["a"], revision: advanced.revision + 1))
            await daemon.hold(false)
            _ = try await settle { false }
            let final = await daemon.state
            check(final.pinned == ["a"] && store.arrangement.pinned == ["a"], "making a pin the daemon already kept again leaves it pinned: \(final) \(store.arrangement)")
        }

        print("tile order store: ok")
    }

    @MainActor
    static func withStore(_ body: (UserDefaults, String) async throws -> Void) async throws {
        let suite = "agenthail.tile-order-test.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        try await body(defaults, suite)
    }

    static func settle(_ done: () async -> Bool) async throws -> Bool {
        for _ in 0..<200 {
            if await done() { return true }
            try await Task.sleep(nanoseconds: 5_000_000)
        }
        return await done()
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
