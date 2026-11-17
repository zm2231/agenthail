import Foundation
import Observation

@MainActor
@Observable
final class ProjectTileOrder {
    static let shared = ProjectTileOrder()
    private static let orderKey = "projectTileOrder"
    private static let pinKey = "projectTilePins"

    @ObservationIgnored private let defaults: UserDefaults
    private(set) var order: [String]
    private(set) var pinned: Set<String>

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        order = defaults.stringArray(forKey: Self.orderKey) ?? []
        pinned = Set(defaults.stringArray(forKey: Self.pinKey) ?? [])
    }

    func tiles(_ projects: [ProjectState], limit: Int) -> ProjectTiles {
        ProjectTiles.arranged(projects, limit: limit, order: order, pinned: pinned)
    }

    func seed(_ ids: [String]) {
        guard order.isEmpty, !ids.isEmpty else { return }
        save(order: ids)
    }

    func isPinned(_ id: String) -> Bool { pinned.contains(id) }

    func togglePin(_ id: String, arranged: [String]) {
        if pinned.contains(id) { pinned.remove(id) } else { pinned.insert(id) }
        defaults.set(pinned.sorted(), forKey: Self.pinKey)
        save(order: complete(arranged))
    }

    func move(_ id: String, before target: String, arranged: [String]) {
        if pinned.contains(target) != pinned.contains(id) { togglePin(id, arranged: arranged) }
        save(order: ProjectTiles.moving(id, before: target, in: complete(arranged)))
    }

    func step(_ id: String, by offset: Int, arranged: [String]) {
        let group = arranged.filter { pinned.contains($0) == pinned.contains(id) }
        guard let index = group.firstIndex(of: id) else { return }
        let next = index + offset
        guard group.indices.contains(next) else { return }
        let all = complete(arranged)
        if offset < 0 {
            save(order: ProjectTiles.moving(id, before: group[next], in: all))
        } else {
            save(order: ProjectTiles.moving(group[next], before: id, in: all))
        }
    }

    private func complete(_ arranged: [String]) -> [String] {
        arranged + order.filter { !arranged.contains($0) }
    }

    private func save(order next: [String]) {
        order = next
        defaults.set(next, forKey: Self.orderKey)
    }
}
