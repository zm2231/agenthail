import Foundation

enum ComposerDrop {
    private static let plain = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "/._-+,:@%="))

    static func text(for urls: [URL]) -> String? {
        guard !urls.isEmpty, urls.allSatisfy(\.isFileURL) else { return nil }
        return urls.map { word(for: $0.path) }.joined(separator: " ")
    }

    static func insert(_ dropped: String, into draft: String) -> String {
        guard !draft.isEmpty else { return dropped }
        return draft.last?.isWhitespace == true ? draft + dropped : "\(draft) \(dropped)"
    }

    private static func word(for path: String) -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        if path == home { return "~" }
        if path.hasPrefix(home + "/") {
            return "~/" + quoted(String(path.dropFirst(home.count + 1)))
        }
        return quoted(path)
    }

    private static func quoted(_ text: String) -> String {
        guard !text.isEmpty, text.unicodeScalars.allSatisfy(plain.contains) else {
            return "'" + text.replacingOccurrences(of: "'", with: "'\\''") + "'"
        }
        return text
    }
}
