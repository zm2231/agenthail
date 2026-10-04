import Foundation

enum ComposerDrop {
    static func text(for urls: [URL]) -> String? {
        let paths = urls.filter(\.isFileURL).map { quoted(($0.path as NSString).abbreviatingWithTildeInPath) }
        return paths.isEmpty ? nil : paths.joined(separator: " ")
    }

    static func insert(_ dropped: String, into draft: String) -> String {
        guard !draft.isEmpty else { return dropped }
        return draft.last?.isWhitespace == true ? draft + dropped : "\(draft) \(dropped)"
    }

    private static func quoted(_ path: String) -> String {
        guard path.contains(where: { $0.isWhitespace || "'\"`$\\".contains($0) }) else { return path }
        return "'" + path.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}
