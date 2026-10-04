import Foundation

enum SessionHandle {
    static func normalized(_ text: String) -> String {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.hasPrefix("@") ? String(trimmed.dropFirst()) : trimmed
    }

    static func problem(with text: String) -> String? {
        let handle = normalized(text)
        if handle.isEmpty { return "Enter a name." }
        if handle.count > 80 { return "Use 80 characters or fewer." }
        if handle.contains(where: { $0.isWhitespace || $0 == "/" || $0 == "#" }) { return "Names can't contain spaces, /, or #." }
        return nil
    }
}
