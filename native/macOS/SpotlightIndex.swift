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
final class SpotlightIndex {
    static let shared = SpotlightIndex()
    static let preferenceKey = "spotlightSessions"
    private static let domain = "sessions"
    private let index = CSSearchableIndex.default()
    private let log = Logger(subsystem: "com.agenthail.app", category: "spotlight")
    private var indexed: [String: SpotlightEntry] = [:]
    private var cleared = false

    func update(_ sessions: [SessionState]) {
        if !cleared {
            cleared = true
            index.deleteSearchableItems(withDomainIdentifiers: [Self.domain], completionHandler: report)
        }
        let changes = SpotlightEntry.changes(from: indexed, to: sessions)
        if !changes.removals.isEmpty {
            index.deleteSearchableItems(withIdentifiers: changes.removals, completionHandler: report)
            changes.removals.forEach { indexed.removeValue(forKey: $0) }
        }
        guard !changes.upserts.isEmpty else { return }
        let items = changes.upserts.map { entry in
            let attributes = CSSearchableItemAttributeSet(contentType: .text)
            attributes.title = entry.title
            attributes.displayName = entry.title
            attributes.contentDescription = entry.detail
            let item = CSSearchableItem(uniqueIdentifier: entry.id, domainIdentifier: Self.domain, attributeSet: attributes)
            item.expirationDate = .distantFuture
            return item
        }
        index.indexSearchableItems(items, completionHandler: report)
        changes.upserts.forEach { indexed[$0.id] = $0 }
    }

    func clear() {
        guard !cleared || !indexed.isEmpty else { return }
        indexed.removeAll()
        cleared = true
        index.deleteSearchableItems(withDomainIdentifiers: [Self.domain], completionHandler: report)
    }

    private nonisolated var report: @Sendable (Error?) -> Void {
        { [log] error in
            if let error { log.error("Spotlight index update failed: \(error.localizedDescription, privacy: .public)") }
        }
    }
}
