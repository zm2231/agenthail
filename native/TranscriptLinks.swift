import Foundation

enum TranscriptLinks {
    private static let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue)
    static let schemes: Set<String> = ["http", "https"]

    static func hasAddress(_ text: String) -> Bool {
        schemes.contains { text.range(of: $0 + "://", options: .caseInsensitive) != nil }
    }

    static func linked(_ text: String) -> AttributedString {
        linked(AttributedString(text))
    }

    static func linked(_ text: AttributedString) -> AttributedString {
        guard let detector, text.characters.contains(":") else { return text }
        var found: [(start: Int, length: Int, url: URL)] = []
        for run in text.runs where run.link == nil && !inCodeBlock(run.presentationIntent) {
            let piece = String(text[run.range].characters)
            let offset = text.characters.distance(from: text.startIndex, to: run.range.lowerBound)
            for match in detector.matches(in: piece, range: NSRange(location: 0, length: (piece as NSString).length)) {
                guard let matched = Range(match.range, in: piece) else { continue }
                let range = trimmed(piece, matched)
                guard !range.isEmpty, let url = range == matched ? match.url : URL(string: String(piece[range])), schemes.contains(url.scheme?.lowercased() ?? ""),
                      schemes.contains(where: { piece[range].lowercased().hasPrefix($0 + "://") }) else { continue }
                found.append((offset + piece.distance(from: piece.startIndex, to: range.lowerBound), piece.distance(from: range.lowerBound, to: range.upperBound), url))
            }
        }
        guard !found.isEmpty else { return text }
        var linked = text
        for link in found {
            let start = linked.characters.index(linked.startIndex, offsetBy: link.start)
            let end = linked.characters.index(start, offsetBy: link.length)
            linked[start ..< end].link = link.url
        }
        return linked
    }

    private static func trimmed(_ piece: String, _ range: Range<String.Index>) -> Range<String.Index> {
        var end = piece[range].firstIndex(of: "`") ?? range.upperBound
        var before = range.lowerBound
        while end > range.lowerBound, before > piece.startIndex, case let mark = piece[piece.index(before: end)], "*_~".contains(mark), piece[piece.index(before: before)] == mark {
            end = piece.index(before: end)
            before = piece.index(before: before)
        }
        return range.lowerBound ..< end
    }

    private static func inCodeBlock(_ intent: PresentationIntent?) -> Bool {
        intent?.components.contains { component in
            if case .codeBlock = component.kind { return true }
            return false
        } ?? false
    }
}
