import Foundation

struct PeerEnvelope: Equatable {
    let sender: String?
    let body: String

    init(_ text: String) {
        var remaining = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if remaining.hasPrefix("[Claude peer message from "), let lineEnd = remaining.firstIndex(of: "\n") {
            remaining = String(remaining[remaining.index(after: lineEnd)...]).trimmingCharacters(in: .whitespacesAndNewlines)
        }
        guard remaining.hasPrefix("<cross-session-message "),
              remaining.hasSuffix("</cross-session-message>"),
              let tagEnd = remaining.firstIndex(of: ">"),
              let sender = Self.attribute("from-name", in: String(remaining[..<tagEnd])) ?? Self.attribute("from", in: String(remaining[..<tagEnd]))
        else {
            self.sender = nil
            self.body = text
            return
        }
        let inner = remaining[remaining.index(after: tagEnd)...].dropLast("</cross-session-message>".count)
        self.sender = sender
        self.body = inner.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private static func attribute(_ name: String, in tag: String) -> String? {
        guard let start = tag.range(of: "\(name)=\"") else { return nil }
        let rest = tag[start.upperBound...]
        guard let end = rest.firstIndex(of: "\"") else { return nil }
        let value = String(rest[..<end])
        return value.isEmpty ? nil : value
    }
}
