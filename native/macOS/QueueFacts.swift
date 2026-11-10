import Foundation

enum QueueFacts {
    static func attachmentNames(_ item: QueueState) -> String? {
        guard item.hasAttachments else { return nil }
        return (item.attachments ?? []).map(\.name).joined(separator: ", ")
    }

    static func statusLabel(_ status: String) -> String {
        switch status {
        case "pending": return "Waiting"
        case "dead": return "Needs a decision"
        case "delivered": return "Delivered"
        case "canceled": return "Canceled"
        default: return status.capitalized
        }
    }

    static func reason(_ item: QueueState, target: SessionState?) -> String {
        switch item.evidence {
        case "transport_accepted": return "Transport accepted; receiver completion is pending"
        case "unknown": return item.isHistorical ? "Delivery was never confirmed" : "Submitted"
        case "failed": return "Delivery failed"
        default: break
        }
        guard item.status == "pending" else { return statusLabel(item.status) }
        if item.heldBy == "hostUnreachable" { return item.holdLabel }
        if let target, target.isReadOnly { return target.readOnlyReason ?? "Target is not writable" }
        if target?.surface == "codex" && target?.status == "notLoaded" {
            return target?.transport == "desktop" ? "Loading this Codex conversation before delivery" : "Codex conversation is not available to load"
        }
        return item.holdLabel
    }

    static func canTakeBack(_ item: QueueState, target: SessionState?) -> Bool {
        guard let target, !target.isReadOnly else { return false }
        return item.status == "pending" && ["", "message"].contains(item.operation ?? "")
    }

    static func canCancel(_ item: QueueState) -> Bool { item.status == "pending" }

    static func lastError(_ item: QueueState) -> String? {
        guard let error = item.lastError?.trimmingCharacters(in: .whitespacesAndNewlines), !error.isEmpty else { return nil }
        return error
    }

    static func detail(_ item: QueueState, target: SessionState?) -> [String] {
        [reason(item, target: target)] + [lastError(item)].compactMap { $0 }
    }

    static func summary(_ item: QueueState, target: SessionState?, age: String) -> String {
        (detail(item, target: target) + ["Queued \(age)"]).joined(separator: " · ")
    }

    static func undelivered(_ queue: [QueueState], for sessionID: String) -> [QueueState] {
        queue.filter { $0.sessionId == sessionID && $0.status != "delivered" }
    }
}
