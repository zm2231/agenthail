import Foundation

enum SessionHandle {
    static func normalized(_ text: String) -> String {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.hasPrefix("@") ? String(trimmed.dropFirst()) : trimmed
    }

    static func problem(with text: String) -> String? {
        let handle = normalized(text)
        if handle.isEmpty { return "Enter a name." }
        if handle.utf8.count > 80 { return "That name is too long." }
        if handle.contains(where: { $0.isWhitespace || $0 == "/" || $0 == "#" }) { return "Names can't contain spaces, /, or #." }
        return nil
    }

    static func clears(_ text: String, current: String?) -> Bool {
        normalized(text).isEmpty && !(current ?? "").isEmpty
    }

    static func leadRefusal(_ handle: String?) -> String {
        let name = handle.map { "@" + normalized($0) } ?? "This name"
        return "\(name) is this project's lead. Change or clear it with Change Lead… or Clear Lead in the project's menu."
    }

    static func renameProblem(_ text: String, current: String?) -> String? {
        clears(text, current: current) ? nil : problem(with: text)
    }
}
