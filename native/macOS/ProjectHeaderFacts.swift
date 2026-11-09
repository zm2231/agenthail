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
}
