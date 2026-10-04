import Foundation

struct PeerEnvelope: Equatable {
    let sender: String?
    let body: String

    init(_ text: String) {
        var remaining = text.trimmingCharacters(in: .whitespacesAndNewlines)
        var sender: String?
        if remaining.hasPrefix("[Claude peer message from "), let close = remaining.firstIndex(of: "]") {
            remaining = String(remaining[remaining.index(after: close)...]).trimmingCharacters(in: .whitespacesAndNewlines)
        }
        if remaining.hasPrefix("<cross-session-message"), let tagEnd = remaining.firstIndex(of: ">") {
            let tag = String(remaining[..<tagEnd])
            sender = Self.attribute("from-name", in: tag) ?? Self.attribute("from", in: tag)
            remaining = String(remaining[remaining.index(after: tagEnd)...])
            if let end = remaining.range(of: "</cross-session-message>", options: .backwards) {
                remaining = String(remaining[..<end.lowerBound])
            }
        }
        self.sender = sender
        self.body = remaining.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private static func attribute(_ name: String, in tag: String) -> String? {
        guard let start = tag.range(of: "\(name)=\"") else { return nil }
        let rest = tag[start.upperBound...]
        guard let end = rest.firstIndex(of: "\"") else { return nil }
        let value = String(rest[..<end])
        return value.isEmpty ? nil : value
    }
}
