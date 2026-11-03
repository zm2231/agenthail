import Foundation

final class TaggedResponses: @unchecked Sendable {
    private let lock = NSLock()
    private var held: [String: (tag: String, data: Data)] = [:]

    func held(_ key: String) -> (tag: String, data: Data)? { lock.withLock { held[key] } }
    func store(_ key: String, tag: String?, data: Data) { lock.withLock { held[key] = tag.map { ($0, data) } } }
}
