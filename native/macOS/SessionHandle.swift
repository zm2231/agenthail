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
}
