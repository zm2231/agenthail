import CoreSpotlight
import Foundation
import os

struct SpotlightEntry: Equatable {
    let id: String
    let title: String
    let detail: String

    init(_ session: SessionState) {
        id = session.id
        title = session.displayName
        let folder = (session.checkout?.path ?? session.cwd).map { URL(fileURLWithPath: $0).lastPathComponent }.flatMap { $0.isEmpty ? nil : $0 }
        detail = [session.surface.capitalized, folder].compactMap { $0 }.joined(separator: " · ")
    }

    static func changes(from indexed: [String: SpotlightEntry], to sessions: [SessionState]) -> (upserts: [SpotlightEntry], removals: [String]) {
        var seen = Set<String>()
        var upserts: [SpotlightEntry] = []
        for session in sessions where seen.insert(session.id).inserted {
            let entry = SpotlightEntry(session)
            if indexed[entry.id] != entry { upserts.append(entry) }
        }
        let removals = indexed.keys.filter { !seen.contains($0) }.sorted()
        return (upserts, removals)
    }
}

@MainActor
protocol SpotlightStore {
    func deleteAll() async throws
    func delete(_ ids: [String]) async throws
    func index(_ entries: [SpotlightEntry]) async throws
}

@MainActor
final class SpotlightSync {
    private enum Target {
        case off
        case sessions([SessionState])
    }

    private let store: SpotlightStore
    private let failed: (Error) -> Void
    private var target: Target?
    private var generation = 0
    private var drainTask: Task<Void, Never>?
    private var cleared = false
    private(set) var indexed: [String: SpotlightEntry] = [:]

    init(store: SpotlightStore, failed: @escaping (Error) -> Void = { _ in }) {
        self.store = store
        self.failed = failed
    }

    func update(_ sessions: [SessionState]) {
        set(.sessions(sessions))
    }

    func clear() {
        set(.off)
    }

    func settle() async {
        await drainTask?.value
    }

    private func set(_ next: Target) {
        target = next
        generation &+= 1
        guard drainTask == nil else { return }
        drainTask = Task {
            await drain()
            drainTask = nil
        }
    }

    private func drain() async {
        var applied = -1
        while applied != generation, let target {
            let current = generation
            do {
                try await apply(target)
                applied = current
            } catch {
                failed(error)
                return
            }
        }
    }

    private func apply(_ target: Target) async throws {
        switch target {
        case .off:
            guard !cleared || !indexed.isEmpty else { return }
            try await store.deleteAll()
            indexed.removeAll()
            cleared = true
        case .sessions(let sessions):
            if !cleared {
                try await store.deleteAll()
                indexed.removeAll()
                cleared = true
            }
            let changes = SpotlightEntry.changes(from: indexed, to: sessions)
            if !changes.removals.isEmpty {
                try await store.delete(changes.removals)
                changes.removals.forEach { indexed.removeValue(forKey: $0) }
            }
            if !changes.upserts.isEmpty {
                try await store.index(changes.upserts)
                changes.upserts.forEach { indexed[$0.id] = $0 }
            }
        }
    }
}

struct CoreSpotlightStore: SpotlightStore {
    private static let domain = "sessions"

    func deleteAll() async throws {
        try await CSSearchableIndex.default().deleteSearchableItems(withDomainIdentifiers: [Self.domain])
    }

    func delete(_ ids: [String]) async throws {
        try await CSSearchableIndex.default().deleteSearchableItems(withIdentifiers: ids)
    }

    func index(_ entries: [SpotlightEntry]) async throws {
        let items = entries.map { entry in
            let attributes = CSSearchableItemAttributeSet(contentType: .text)
            attributes.title = entry.title
            attributes.displayName = entry.title
            attributes.contentDescription = entry.detail
            let item = CSSearchableItem(uniqueIdentifier: entry.id, domainIdentifier: Self.domain, attributeSet: attributes)
            item.expirationDate = .distantFuture
            return item
        }
        try await CSSearchableIndex.default().indexSearchableItems(items)
    }
}

@MainActor
enum SpotlightIndex {
    static let preferenceKey = "spotlightSessions"
    private static let log = Logger(subsystem: "com.agenthail.app", category: "spotlight")
    static let shared = SpotlightSync(store: CoreSpotlightStore()) { error in
        log.error("Spotlight index update failed: \(error.localizedDescription, privacy: .public)")
    }
}
