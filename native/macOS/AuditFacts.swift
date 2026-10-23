import Foundation

enum AuditFacts {
    static func kindLabel(_ kind: String) -> String {
        let words = (kind.isEmpty ? "event" : kind)
            .split(whereSeparator: { $0 == "_" || $0 == "-" || $0 == " " })
            .joined(separator: " ")
            .lowercased()
        return words.prefix(1).uppercased() + words.dropFirst()
    }

    static func heading(_ entry: HistoryState) -> String {
        guard let operation = nonEmpty(entry.operation), operation != "message" else { return kindLabel(entry.kind) }
        return "\(kindLabel(entry.kind)) · \(kindLabel(operation))"
    }

    static func target(_ entry: HistoryState) -> String {
        nonEmpty(entry.target) ?? nonEmpty(entry.sessionId) ?? "Agenthail"
    }

    static func source(_ entry: HistoryState) -> String? {
        guard let source = nonEmpty(entry.source), source != target(entry) else { return nil }
        return source
    }

    static func isError(_ entry: HistoryState) -> Bool {
        nonEmpty(entry.error) != nil
    }

    static func content(_ entry: HistoryState) -> String {
        if let error = nonEmpty(entry.error) { return error }
        if let message = nonEmpty(entry.message) { return message }
        return nonEmpty(entry.result) ?? "No message content recorded"
    }

    private static func nonEmpty(_ value: String?) -> String? {
        guard let value, !value.isEmpty else { return nil }
        return value
    }
}
