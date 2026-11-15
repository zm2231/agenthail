import Foundation

enum ProjectWork {
    static let pageID = "ledger"

    struct Place: Equatable {
        let hostID: String
        let projectID: String
    }

    enum Page: Equatable {
        case reading
        case shown(URL)
        case unavailable(String)
    }

    // Ledger sync keeps the copies equal; the nearest needs no forwarding.
    static func place(_ project: ProjectState, preferring near: String) -> Place {
        let checkouts = project.checkouts.map { ($0.host.state == "local" ? near : $0.host.id, $0.projectId) }
        func local(_ id: String, on host: String) -> String {
            let prefix = host + "::"
            return id.hasPrefix(prefix) ? String(id.dropFirst(prefix.count)) : id
        }
        func at(_ host: String) -> Place? {
            checkouts.first { $0.0 == host }.map { Place(hostID: host, projectID: local($0.1, on: host)) }
        }
        if let here = at(near) { return here }
        if let lead = project.leads.first {
            let host = lead.host.flatMap { $0.state == "local" ? nil : $0.id } ?? near
            return at(host) ?? Place(hostID: host, projectID: local(project.id, on: host))
        }
        if let first = checkouts.first, let place = at(first.0) { return place }
        return Place(hostID: near, projectID: project.id)
    }

    static func page(_ list: ExtensionsList?, projectID: String, origin: URL, hostName: String) -> Page {
        guard let list else { return .reading }
        guard let builtIn = list.builtIn else { return .unavailable("\(hostName) doesn't serve the work list yet. Update Agenthail there.") }
        guard let page = builtIn[pageID] else {
            return .unavailable(list.host == "off" ? "Extensions are off on \(hostName), and the work list is served with them." : "\(hostName) hasn't served its work list yet. If this lasts, update Agenthail there.")
        }
        let (base, unavailable) = ExtensionEntries.target(port: page.port, entry: page.entry, published: page.published, error: page.error, origin: origin, hostName: hostName)
        guard let base, var parts = URLComponents(url: base, resolvingAgainstBaseURL: false) else {
            return .unavailable(unavailable ?? "The work list can't open right now.")
        }
        let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~:/")
        let value = projectID.addingPercentEncoding(withAllowedCharacters: allowed) ?? projectID
        parts.percentEncodedQuery = [parts.percentEncodedQuery, "project=" + value].compactMap { $0 }.joined(separator: "&")
        guard let url = parts.url else { return .unavailable("This address is not valid.") }
        return .shown(url)
    }

    static let placeholderHeight: CGFloat = 120

    static func controllerID(_ place: Place) -> String { "work/\(place.hostID)/\(place.projectID)" }
}
