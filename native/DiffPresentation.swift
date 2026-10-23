import SwiftUI

/// One row of a file's diff on screen, numbered across its hunks so the rows can be listed lazily.
struct DiffEntry: Identifiable, Equatable {
    enum Content: Equatable {
        case line(TextDiff.Line)
        case folded(Int)
        case hunk
    }

    let id: Int
    let content: Content

    static func entries(_ hunks: [TextDiff]) -> [DiffEntry] {
        var contents: [Content] = []
        for (index, hunk) in hunks.enumerated() {
            if index > 0 { contents.append(.hunk) }
            for row in hunk.rows() {
                switch row {
                case .line(let line): contents.append(.line(line))
                case .folded(let count): contents.append(.folded(count))
                }
            }
        }
        return contents.enumerated().map { DiffEntry(id: $0.offset, content: $0.element) }
    }
}

extension TextDiff.Kind {
    var sign: String {
        switch self {
        case .added: "+"
        case .removed: "−"
        case .same: " "
        }
    }

    var spoken: String {
        switch self {
        case .added: "Added: "
        case .removed: "Removed: "
        case .same: ""
        }
    }
}

extension TextDiff.Line {
    func attributed(marking: Color?) -> AttributedString {
        var text = AttributedString(self.text)
        guard let marking else { return text }
        let count = text.characters.count
        for mark in marks where mark.upperBound <= count {
            let start = text.characters.index(text.startIndex, offsetBy: mark.lowerBound)
            let end = text.characters.index(start, offsetBy: mark.count)
            text[start..<end].backgroundColor = marking
        }
        return text
    }
}
