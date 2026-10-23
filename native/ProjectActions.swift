import Foundation

struct ProjectState: Decodable, Hashable, Identifiable {
    struct Checkout: Decodable, Hashable {
        let host: SessionHost
        let projectId: String
        let rootPath: String
        var added: Bool? = nil
        var archived: Bool? = nil
    }

    struct Lead: Decodable, Hashable {
        var host: SessionHost? = nil
        let name: String
        let surface: String
        /// Empty until the lead's first message starts its conversation.
        var sessionId: String? = nil
    }

    let id: String
    let displayName: String
    var checkouts: [Checkout] = []
    var coordinators: [Lead]? = nil
    var archived: Bool? = nil

    var leads: [Lead] { coordinators ?? [] }
}

struct ProjectLead: Decodable, Hashable {
    let name: String
    let projectRoot: String
}

enum ProjectMenuItem: Hashable {
    case rename
    case newConversation(checkout: Int, label: String)
    case lead(change: Bool)
    case clearLead
    case remove(projectID: String, label: String)
}

enum ProjectMenu {
    // A checkout is named by its host, never by its folder: menus must not reveal paths.
    static func items(for project: ProjectState) -> [ProjectMenuItem] {
        let checkouts = project.checkouts
        let several = Set(checkouts.map(\.host.id)).count > 1
        func place(_ checkout: ProjectState.Checkout) -> String {
            several || checkout.host.state != "local" ? " on \(shortHostName(checkout.host.name))" : ""
        }
        var items: [ProjectMenuItem] = []
        if checkouts.contains(where: { $0.added == true }) { items.append(.rename) }
        if project.archived != true {
            for (index, checkout) in checkouts.enumerated() where checkout.archived != true {
                items.append(.newConversation(checkout: index, label: "New conversation" + place(checkout)))
            }
            items.append(.lead(change: !project.leads.isEmpty))
            if project.leads.count == 1 { items.append(.clearLead) }
        }
        for checkout in checkouts where checkout.added == true {
            items.append(.remove(projectID: checkout.projectId, label: "Remove" + place(checkout)))
        }
        return items
    }

    static func host(of checkout: ProjectState.Checkout) -> String? {
        checkout.host.state == "local" ? nil : checkout.host.id
    }

    static func host(of session: SessionState) -> String? {
        guard let host = session.host, host.state != "local" else { return nil }
        return host.id
    }

    static func folderSuggestions(_ options: SessionCreationOptions?) -> [String] {
        var seen = Set<String>()
        let roots = (options?.projects ?? []).filter { $0.added != true }.map(\.rootPath)
        return (roots + (options?.folders ?? [])).filter { seen.insert($0).inserted }
    }

    static func canAddFolder(_ session: SessionState) -> Bool {
        guard let cwd = session.cwd, !cwd.isEmpty else { return false }
        return !(session.project?.added == true && session.project?.rootPath == cwd)
    }

    static func canMakeLead(_ session: SessionState, projects: [ProjectState]) -> Bool {
        guard let project = session.project, session.subagent == nil, session.surface == "claude" || session.surface == "codex" else { return false }
        return !projects.contains { $0.id == project.id && $0.leads.contains { $0.sessionId == session.id } }
    }

    static func shortHostName(_ name: String) -> String {
        String(name.split(separator: ".", maxSplits: 1).first ?? Substring(name))
    }
}

struct ProjectActionError: LocalizedError, Equatable {
    let status: Int
    let code: String
    let message: String

    init(status: Int, data: Data) {
        let error = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"]
        let fields = error as? [String: Any]
        self.status = status
        code = fields?["code"] as? String ?? ""
        message = fields?["message"] as? String ?? (error as? String) ?? HTTPURLResponse.localizedString(forStatusCode: status)
    }

    var needsReplace: Bool { code == "coordinator_exists" || code == "coordinator_conflict" }

    var errorDescription: String? { message }
}

enum ProjectChange: Equatable {
    case done(String)
    case confirmReplace(String)
    case failed(String)

    static func run(isolation: isolated (any Actor)? = #isolation, _ body: () async throws -> String) async -> ProjectChange {
        do {
            return .done(try await body())
        } catch let error as ProjectActionError where error.needsReplace {
            return .confirmReplace(error.message + ". Replace it?")
        } catch {
            return .failed(AgenthailAPIError.userMessage(error))
        }
    }
}

enum ProjectSheet: Identifiable, Equatable {
    case add(host: String?, folder: String)
    case rename(ProjectState)
    case lead(ProjectState)

    var id: String {
        switch self {
        case .add: return "add"
        case .rename(let project): return "rename:" + project.id
        case .lead(let project): return "lead:" + project.id
        }
    }
}

struct ProjectConfirmation: Identifiable {
    let id = UUID()
    let message: String
    let action: String
    let run: () async -> ProjectChange
}
