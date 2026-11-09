import Foundation

enum ProjectLeadChip: Equatable {
    case conflict(label: String, help: String)
    case waiting(label: String, help: String)
    case open(label: String, help: String, sessionID: String)
}

enum ProjectHeaderFacts {
    static func ref(_ lead: ProjectState.Lead) -> String {
        guard let host = lead.host, host.state != "local" else { return "@" + lead.name }
        return "\(HostNames.short(host.name))::@\(lead.name)"
    }

    static func leadChip(_ leads: [ProjectState.Lead]) -> ProjectLeadChip? {
        guard let first = leads.first else { return nil }
        if leads.count > 1 {
            let refs = leads.map(ref).joined(separator: ", ")
            return .conflict(label: "\(leads.count) leads", help: "Several hosts each have a coordinator: \(refs). Change the lead to keep one.")
        }
        let name = ref(first)
        guard let sessionID = ProjectContents.leadSessionID(first) else {
            return .waiting(label: name, help: "\(name) starts on its first message")
        }
        return .open(label: name, help: "Open \(name), the project coordinator", sessionID: sessionID)
    }

    static func broadcastPrompt(_ project: ProjectState) -> String {
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
        return notice
    }
}
