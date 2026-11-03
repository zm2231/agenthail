import Foundation

@MainActor
final class SpotlightCatalog {
    static let scopes = ["all", "archived"]

    private(set) var entries: [String: SpotlightEntry] = [:]
    private(set) var walkedEpoch: String?
    private var walk: Task<Void, Never>?
    private var touched: Set<String> = []
    private let api: () -> AgenthailAPI?
    private let publish: ([SpotlightEntry]) -> Void
    private let priority: TaskPriority

    init(priority: TaskPriority = .utility, api: @escaping () -> AgenthailAPI?, publish: @escaping ([SpotlightEntry]) -> Void) {
        self.priority = priority
        self.api = api
        self.publish = publish
    }

    var walking: Bool { walk != nil }

    func epoch(_ epoch: String?) {
        guard let epoch, epoch != walkedEpoch else { return }
        walkedEpoch = epoch
        walk?.cancel()
        touched = []
        walk = Task(priority: priority) { [weak self] in
            await self?.walkAll(epoch: epoch)
        }
    }

    func apply(upserted: [SessionState], removed: [String]) {
        guard !upserted.isEmpty || !removed.isEmpty else { return }
        for session in upserted {
            entries[session.id] = SpotlightEntry(session)
            touched.insert(session.id)
        }
        for id in removed {
            entries.removeValue(forKey: id)
            touched.insert(id)
        }
        if walk == nil { publish(Array(entries.values)) }
    }

    func stop() {
        walk?.cancel()
        walk = nil
        walkedEpoch = nil
        entries = [:]
        touched = []
    }

    private func walkAll(epoch: String) async {
        var walked: [String: SpotlightEntry] = [:]
        for scope in Self.scopes {
            var cursor: String?
            repeat {
                guard !Task.isCancelled, let api = api() else { return finish(epoch, nil) }
                guard let page = try? await api.scopePage(ScopeRequest(scope: scope), cursor: cursor, limit: 200) else { return finish(epoch, nil) }
                for session in page.sessions { walked[session.id] = SpotlightEntry(session) }
                cursor = page.nextCursor
                await Task.yield()
            } while cursor != nil
        }
        finish(epoch, walked)
    }

    private func finish(_ epoch: String, _ walked: [String: SpotlightEntry]?) {
        guard walkedEpoch == epoch, !Task.isCancelled else { return }
        walk = nil
        guard var walked else {
            walkedEpoch = nil
            return
        }
        for id in touched { walked[id] = entries[id] }
        entries = walked
        touched = []
        publish(Array(entries.values))
    }
}
