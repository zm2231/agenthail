import CryptoKit
import Foundation

struct SessionHolder: Codable, Hashable, Identifiable {
    let pid: Int
    var tty: String? = nil
    let status: String
    var startedAt: String? = nil

    var id: Int { pid }

    var label: String {
        ["pid \(pid)", tty, status].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: ", ")
    }

    /// Sent with the pid so a process that reused it since the pick is refused.
    var startedAtMilliseconds: Int64? {
        guard let startedAt, let date = Self.parse(startedAt) else { return nil }
        return Int64((date.timeIntervalSince1970 * 1000).rounded())
    }

    private static func parse(_ text: String) -> Date? {
        TimestampParser.date(text)
    }
}

extension SessionState {
    var sharedHolders: [SessionHolder] {
        guard surface == "claude", let holders, holders.count > 1 else { return [] }
        return holders
    }
}

struct HolderPin: Hashable, Sendable {
    let pid: Int
    let startedAt: Int64?

    init(pid: Int, startedAt: Int64?) {
        self.pid = pid
        self.startedAt = startedAt
    }

    init(_ holder: SessionHolder) {
        self.init(pid: holder.pid, startedAt: holder.startedAtMilliseconds)
    }
}

/// A pick lasts only while its conversation still lists that process among
/// several holders, so a stale pick never addresses a conversation that one
/// process holds again.
final class HolderPins: @unchecked Sendable {
    static let shared = HolderPins()

    private let lock = NSLock()
    private var pins: [String: HolderPin] = [:]

    func pin(for sessionID: String) -> HolderPin? {
        lock.withLock { pins[sessionID] }
    }

    func pick(_ pin: HolderPin?, for sessionID: String) {
        lock.withLock { pins[sessionID] = pin }
    }

    var all: [String: HolderPin] {
        lock.withLock { pins }
    }

    func prune(to sessions: [SessionState]) {
        let shared = Dictionary(sessions.map { ($0.id, $0.sharedHolders) }, uniquingKeysWith: { first, _ in first })
        lock.withLock {
            pins = pins.filter { sessionID, pin in
                shared[sessionID, default: []].contains { HolderPin($0) == pin }
            }
        }
    }

    /// The same exclusions as the web dashboard: these name a session without
    /// writing to one of its processes.
    static let unpinnedActions: Set<String> = ["session-create", "notion-create", "queue-retry", "queue-cancel", "delivery-dismiss"]
    static let unpinnedPrefixes = ["channel-", "relay-", "project-"]

    /// The daemon keeps a receipt per key, refusals included, so the key must
    /// change with the pick: retrying after picking a terminal is a new request.
    func pinned(body: Data, key: String) -> (body: Data, key: String) {
        guard var request = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
              let action = request["action"] as? String,
              let sessionID = request["sessionId"] as? String,
              !Self.unpinnedActions.contains(action),
              !Self.unpinnedPrefixes.contains(where: action.hasPrefix),
              let pin = pin(for: sessionID),
              let pinnedBody = try? JSONSerialization.data(withJSONObject: {
                  request["pid"] = pin.pid
                  if let startedAt = pin.startedAt { request["pidStartedAt"] = startedAt }
                  return request
              }(), options: [.sortedKeys])
        else { return (body, key) }
        return (pinnedBody, Self.key(key, pinnedTo: pin))
    }

    static func key(_ key: String, pinnedTo pin: HolderPin) -> String {
        let digest = SHA256.hash(data: Data("\(key)|pid=\(pin.pid)|start=\(pin.startedAt ?? 0)".utf8))
        var bytes = Array(digest.prefix(16))
        bytes[6] = (bytes[6] & 0x0F) | 0x50
        bytes[8] = (bytes[8] & 0x3F) | 0x80
        let uuid = bytes.withUnsafeBufferPointer { UUID(uuid: (
            $0[0], $0[1], $0[2], $0[3], $0[4], $0[5], $0[6], $0[7],
            $0[8], $0[9], $0[10], $0[11], $0[12], $0[13], $0[14], $0[15]
        )) }
        return uuid.uuidString
    }
}
