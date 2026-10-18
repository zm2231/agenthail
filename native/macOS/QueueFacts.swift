import Foundation

enum QueueFacts {
    static func statusLabel(_ status: String) -> String {
        switch status {
        case "pending": return "Waiting"
        case "dead": return "Needs a decision"
        case "delivered": return "Delivered"
        case "expired": return "Expired"
        case "canceled": return "Canceled"
        default: return status.capitalized
        }
    }

    static func reason(_ item: QueueState, target: SessionState?) -> String {
        switch item.evidence {
        case "transport_accepted": return "Transport accepted; receiver completion is pending"
        case "unknown": return "Delivery outcome is unknown"
        case "failed": return "Delivery failed"
        default: break
        }
        guard item.status == "pending" else { return statusLabel(item.status) }
        if let target, target.isReadOnly { return target.readOnlyReason ?? "Target is not writable" }
        if target?.surface == "codex" && target?.status == "notLoaded" {
            return target?.transport == "desktop" ? "Loading this Codex conversation before delivery" : "Codex conversation is not available to load"
        }
        return target?.status == "busy" ? "Waiting for this agent to finish" : "Waiting for delivery"
    }

    static func canRetry(_ item: QueueState, target: SessionState?) -> Bool {
        guard let target, !target.isReadOnly else { return false }
        return item.status == "dead"
    }

    static func canCancel(_ item: QueueState) -> Bool { item.status == "pending" || item.status == "dead" }

    static func lastError(_ item: QueueState) -> String? {
        guard let error = item.lastError?.trimmingCharacters(in: .whitespacesAndNewlines), !error.isEmpty else { return nil }
        return error
    }

    static func attention(_ items: [AttentionState], for sessionID: String) -> [AttentionState] {
        items.filter { $0.sessionId == sessionID }
    }

    static func attention(_ items: [AttentionState], forQueueItem id: Int64) -> AttentionState? {
        items.first { $0.queueId == id }
    }

    static func detail(_ item: QueueState, target: SessionState?, attention: [AttentionState]) -> [String] {
        let reason = QueueFacts.attention(attention, forQueueItem: item.id).map { [$0.reason, $0.requestedAction] } ?? [reason(item, target: target)]
        return reason + [lastError(item)].compactMap { $0 }
    }

    static func undelivered(_ queue: [QueueState], for sessionID: String) -> [QueueState] {
        queue.filter { $0.sessionId == sessionID && $0.status != "delivered" }
    }
}
