import Foundation

enum SessionNames {
    static func display(_ name: String) -> String {
        guard let match = name.prefixMatch(of: #/https?://[^/]+/([^/\s]+)/([^/\s]+)/#) else { return name }
        return "\(match.1) / \(match.2)"
    }
}
