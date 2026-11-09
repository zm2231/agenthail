import Foundation

enum ProjectBroadcast {
    static func prompt(_ project: ProjectState) -> String {
        "Message every active conversation in \(project.displayName)"
    }

    static func sent(_ result: ChannelSendResult, name: (String) -> String) -> String {
        func label(_ recipient: ChannelSendResult.Recipient) -> String {
            recipient.coordinator.flatMap { $0.isEmpty ? nil : $0 } ?? name(recipient.sessionId ?? "")
        }
        let reached = result.recipients.filter { $0.outcome != "failed" }.map(label)
        let missed = result.recipients.filter { $0.outcome == "failed" }.map { recipient in
            [label(recipient), recipient.detail.flatMap { $0.isEmpty ? nil : $0 }].compactMap { $0 }.joined(separator: ": ")
        }
        var notice = "Sent to \(reached.count)\(reached.isEmpty ? "" : ": " + reached.joined(separator: ", "))."
        if !missed.isEmpty {
            notice += " Not delivered to \(missed.joined(separator: "; "))."
        }
        if let unreachable = result.unreachable, !unreachable.isEmpty {
            notice += " Held for \(unreachable.joined(separator: ", ")) until it answers."
        }
        let refused = (result.hosts ?? []).filter { $0.state == "refused" }.map { host in
            [host.name, host.detail.flatMap { $0.isEmpty ? nil : $0 }].compactMap { $0 }.joined(separator: ": ")
        }
        if !refused.isEmpty {
            notice += " Refused by \(refused.joined(separator: "; "))."
        }
        let unreached = (result.hosts ?? []).filter { $0.state == "unreached" }.map(\.name)
        if !unreached.isEmpty {
            notice += " Couldn't reach \(unreached.joined(separator: ", "))."
        }
        return notice
    }
}
