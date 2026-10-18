import Foundation

enum AuditFacts {
    static func kindLabel(_ kind: String) -> String {
        (kind.isEmpty ? "event" : kind).replacingOccurrences(of: "_", with: " ").capitalized
    }

    static func target(_ entry: HistoryState) -> String {
        nonEmpty(entry.target) ?? nonEmpty(entry.sessionId) ?? "Agenthail"
    }

    static func source(_ entry: HistoryState) -> String? {
        nonEmpty(entry.source)
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
