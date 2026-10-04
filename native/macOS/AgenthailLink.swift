import Foundation

enum AgenthailLink: Equatable {
    case open
    case newSession
    case session(String)

    init?(url: URL) {
        guard url.scheme?.lowercased() == "agenthail", url.query == nil, url.fragment == nil, url.user == nil, url.port == nil else { return nil }
        let parts = url.pathComponents.filter { $0 != "/" }
        switch url.host?.lowercased() ?? "" {
        case "", "open":
            guard parts.isEmpty else { return nil }
            self = .open
        case "new":
            guard parts.isEmpty else { return nil }
            self = .newSession
        case "session":
            guard parts.count == 1 else { return nil }
            let reference = parts[0].trimmingCharacters(in: .whitespacesAndNewlines)
            guard !reference.isEmpty else { return nil }
            self = .session(reference)
        default:
            return nil
        }
    }

    static func matches(_ session: SessionState, reference: String) -> Bool {
        if session.id == reference { return true }
        guard let alias = session.alias, !alias.isEmpty else { return false }
        let bare = reference.hasPrefix("@") ? String(reference.dropFirst()) : reference
        return alias.caseInsensitiveCompare(bare) == .orderedSame
    }
}
