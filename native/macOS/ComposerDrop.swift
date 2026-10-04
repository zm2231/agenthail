import Foundation
import UniformTypeIdentifiers

enum ComposerDrop {
    private static let plain = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "/._-+,:@%="))

    static func files(_ urls: [URL]) -> [URL]? {
        guard !urls.isEmpty, urls.allSatisfy(\.isFileURL) else { return nil }
        return urls
    }

    static func adding(_ urls: [URL], to attachments: [URL]) -> [URL] {
        urls.reduce(into: attachments) { result, url in
            if !result.contains(where: { $0.standardizedFileURL == url.standardizedFileURL }) { result.append(url) }
        }
    }

    static func isEmpty(text: String, attachments: [URL]) -> Bool {
        attachments.isEmpty && text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    static func message(text: String, attachments: [URL]) -> String {
        guard !attachments.isEmpty else { return text }
        let paths = attachments.map { word(for: $0.path) }.joined(separator: "\n")
        let typed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return typed.isEmpty ? paths : "\(typed)\n\n\(paths)"
    }

    static func isImage(_ url: URL) -> Bool {
        UTType(filenameExtension: url.pathExtension)?.conforms(to: .image) == true
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
