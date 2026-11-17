import Foundation
import Observation

struct TileOrderState: Codable, Equatable, Sendable {
    var order: [String]
    var pinned: [String]
    var revision: Int64
}

enum TileWrite: Equatable, Sendable {
    case saved(TileOrderState)
    case stale(TileOrderState)
}

enum TileChange: Codable, Equatable {
    case pin(String, Bool)
    case move(String, before: String)
    case step(String, by: Int)
}

struct TileArrangement: Equatable {
    var order: [String] = []
    var pinned: Set<String> = []

    func applying(_ change: TileChange, arranged: [String]) -> TileArrangement {
        let all = arranged + order.filter { !arranged.contains($0) }
        var next = self
        switch resolving(change, arranged: arranged) {
        case .pin(let id, let on)?:
            if on { next.pinned.insert(id) } else { next.pinned.remove(id) }
            next.order = all
        case .move(let id, let target)?:
            if pinned.contains(target) { next.pinned.insert(id) } else { next.pinned.remove(id) }
            next.order = ProjectTiles.moving(id, before: target, in: all)
        case .step?, nil:
            return self
        }
        return next
    }

    func resolving(_ change: TileChange, arranged: [String]) -> TileChange? {
        guard case .step(let id, let offset) = change else { return change }
        let group = arranged.filter { pinned.contains($0) == pinned.contains(id) }
        guard let index = group.firstIndex(of: id), group.indices.contains(index + offset) else { return nil }
        let other = group[index + offset]
        return offset < 0 ? .move(id, before: other) : .move(other, before: id)
    }
}

@MainActor
@Observable
final class TileOrderStore {
    struct IDs {
        var inward: (String) -> String = { $0 }
        var outward: (String) -> String = { $0 }
    }

    private(set) var arrangement: TileArrangement

    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private let key: String
    @ObservationIgnored private var revision: Int64
    @ObservationIgnored private var unsent: Bool
    @ObservationIgnored private var sending = false
    @ObservationIgnored private var pending: [TileChange] = []
    @ObservationIgnored private var heard = false
    @ObservationIgnored private var write: (@Sendable (TileOrderState) async throws -> TileWrite)?
    @ObservationIgnored private var ids = IDs()
    @ObservationIgnored private var arrange: (TileArrangement) -> [String] = { $0.order }

    init(defaults: UserDefaults = .standard, key: String, legacy: (order: String, pinned: String)? = nil) {
        self.defaults = defaults
        self.key = key
        if let legacy, defaults.object(forKey: key + ".order") == nil, let order = defaults.stringArray(forKey: legacy.order) {
            defaults.set(order, forKey: key + ".order")
            defaults.set(defaults.stringArray(forKey: legacy.pinned) ?? [], forKey: key + ".pinned")
        }
        arrangement = TileArrangement(order: defaults.stringArray(forKey: key + ".order") ?? [], pinned: Set(defaults.stringArray(forKey: key + ".pinned") ?? []))
        revision = (defaults.object(forKey: key + ".revision") as? NSNumber)?.int64Value ?? 0
        unsent = defaults.bool(forKey: key + ".unsent")
        pending = defaults.data(forKey: key + ".pending").flatMap { try? JSONDecoder().decode([TileChange].self, from: $0) } ?? []
    }

    func tiles(_ projects: [ProjectState], limit: Int) -> ProjectTiles {
        arrange = { arrangement in
            let tiles = ProjectTiles.arranged(projects, limit: limit, order: arrangement.order, pinned: arrangement.pinned)
            return (tiles.shown + tiles.more).map(\.id)
        }
        return ProjectTiles.arranged(projects, limit: limit, order: arrangement.order, pinned: arrangement.pinned)
    }

    func isPinned(_ id: String) -> Bool { arrangement.pinned.contains(id) }

    func moves(_ change: TileChange, arranged: [String]) -> Bool {
        arrangement.applying(change, arranged: arranged).order != arranged + arrangement.order.filter { !arranged.contains($0) }
    }

    func seed(_ ids: [String]) {
        guard arrangement.order.isEmpty, !ids.isEmpty else { return }
        save(TileArrangement(order: ids, pinned: arrangement.pinned), unsent: unsent)
        if heard && revision == 0 { send() }
    }

    func change(_ change: TileChange, arranged: [String]) {
        guard let change = arrangement.resolving(change, arranged: arranged) else { return }
        let next = arrangement.applying(change, arranged: arranged)
        guard next != arrangement else { return }
        pending.append(change)
        save(next, unsent: true)
        send()
    }

    func adopt(_ remote: TileOrderState?, ids: IDs = IDs(), write: @escaping @Sendable (TileOrderState) async throws -> TileWrite) {
        self.ids = ids
        self.write = write
        guard let remote else { return }
        heard = true
        guard !sending, unsent || remote.revision >= revision else { return }
        if unsent || remote.revision == 0 && !arrangement.order.isEmpty {
            revision = remote.revision
            send()
        } else if remote.revision != revision || inward(remote) != arrangement {
            take(remote)
        }
    }

    private func send() {
        guard heard, let write, !sending else { return }
        sending = true
        Task {
            var retried = false
            while true {
                let sent = arrangement
                let included = pending.count
                do {
                    switch try await write(outward(sent)) {
                    case .saved(let saved):
                        pending.removeFirst(min(included, pending.count))
                        revision = saved.revision
                        persist()
                        guard arrangement == sent else { continue }
                        save(arrangement, unsent: false)
                    case .stale(let current) where retried || !unsent:
                        pending = []
                        take(current)
                    case .stale(let current):
                        retried = true
                        revision = current.revision
                        let base = inward(current)
                        save(pending.isEmpty ? arrangement : pending.reduce(base) { $0.applying($1, arranged: arrange($0)) }, unsent: true)
                        continue
                    }
                } catch {}
                sending = false
                return
            }
        }
    }

    private func take(_ remote: TileOrderState) {
        revision = remote.revision
        save(inward(remote), unsent: false)
    }

    private func inward(_ state: TileOrderState) -> TileArrangement {
        TileArrangement(order: state.order.map(ids.inward), pinned: Set(state.pinned.map(ids.inward)))
    }

    private func outward(_ arrangement: TileArrangement) -> TileOrderState {
        TileOrderState(order: arrangement.order.map(ids.outward), pinned: arrangement.pinned.map(ids.outward).sorted(), revision: revision)
    }

    private func save(_ next: TileArrangement, unsent: Bool) {
        if arrangement != next { arrangement = next }
        self.unsent = unsent
        persist()
    }

    private func persist() {
        defaults.set(arrangement.order, forKey: key + ".order")
        defaults.set(arrangement.pinned.sorted(), forKey: key + ".pinned")
        defaults.set(NSNumber(value: revision), forKey: key + ".revision")
        defaults.set(unsent, forKey: key + ".unsent")
        defaults.set(try? JSONEncoder().encode(pending), forKey: key + ".pending")
    }
}
