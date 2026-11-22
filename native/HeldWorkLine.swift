import Foundation

struct HeldWorkLog: Codable, Hashable, Sendable {
    let type: String
    let text: String
    let at: String?
}

struct HeldWork: Codable, Hashable, Sendable {
    let itemId: String
    let text: String
    let state: String?
    var projectKey: String? = nil
    var log: HeldWorkLog? = nil
}

struct HeldWorkLine: Equatable, Sendable {
    let id: String
    let text: String
    let help: String

    static func of(_ session: SessionState) -> HeldWorkLine? {
        guard let held = session.heldWork, let work = held.first else { return nil }
        let latest = work.log.map { "\($0.type): \($0.text)" } ?? work.text
        let more = held.count > 1 ? " +\(held.count - 1)" : ""
        return HeldWorkLine(id: work.itemId + more, text: latest, help: work.itemId + " " + work.text + (work.log == nil ? "" : "\n" + latest))
    }

    var spoken: String { "Holds \(id): \(text)" }
}
