import Foundation

/// A unified diff, line by line. A removed line followed by its replacement marks the words that changed.
struct TextDiff: Equatable {
    enum Kind: Equatable {
        case same
        case added
        case removed
    }

    struct Line: Equatable {
        let kind: Kind
        let text: String
        /// Character offsets of the words that changed, when the line pairs with a replacement.
        var marks: [Range<Int>] = []
    }

    enum Row: Equatable {
        case line(Line)
        case folded(Int)
    }

    private(set) var lines: [Line]

    var added: Int { lines.count { $0.kind == .added } }
    var removed: Int { lines.count { $0.kind == .removed } }

    /// Lines longer than this are not matched word by word.
    static let markLimit = 1000

    init(lines: [Line]) {
        self.lines = lines
    }

    init(before: String, after: String) {
        let old = FileChange.lines(before).map(String.init)
        let new = FileChange.lines(after).map(String.init)
        self.init(lines: Self.matched(old, new))
    }

    /// Changes with `context` unchanged lines around each, the words that changed marked; longer unchanged stretches fold.
    func rows(context: Int = 3) -> [Row] {
        let lines = Self.marked(lines)
        var rows: [Row] = []
        var index = 0
        while index < lines.count {
            guard lines[index].kind == .same else {
                rows.append(.line(lines[index]))
                index += 1
                continue
            }
            var end = index
            while end < lines.count, lines[end].kind == .same { end += 1 }
            let leading = index == 0 ? 0 : context
            let trailing = end == lines.count ? 0 : context
            if end - index > leading + trailing + 1 {
                rows += lines[index..<(index + leading)].map(Row.line)
                rows.append(.folded(end - index - leading - trailing))
                rows += lines[(end - trailing)..<end].map(Row.line)
            } else {
                rows += lines[index..<end].map(Row.line)
            }
            index = end
        }
        return rows
    }

    private static func matched(_ old: [String], _ new: [String]) -> [Line] {
        let difference = new.difference(from: old)
        let removed = Set(difference.removals.map(\.offset))
        let inserted = Set(difference.insertions.map(\.offset))
        var lines: [Line] = []
        var i = 0
        var j = 0
        while i < old.count || j < new.count {
            if i < old.count, removed.contains(i) || j == new.count {
                lines.append(Line(kind: .removed, text: old[i]))
                i += 1
            } else if j < new.count, inserted.contains(j) || i == old.count {
                lines.append(Line(kind: .added, text: new[j]))
                j += 1
            } else {
                lines.append(Line(kind: .same, text: new[j]))
                i += 1
                j += 1
            }
        }
        return lines
    }

    /// How much of a removed line must survive in an added one for the two to pair as an edit.
    static let pairSimilarity = 0.25
    /// Added lines a removed line is compared with, so a long replaced block stays cheap to mark.
    static let pairWindow = 8

    /// Pairs each removed line with the most similar added line after it, keeping order, and marks the words that differ.
    private static func marked(_ lines: [Line]) -> [Line] {
        var lines = lines
        var index = 0
        while index < lines.count {
            guard lines[index].kind == .removed else {
                index += 1
                continue
            }
            var removedEnd = index
            while removedEnd < lines.count, lines[removedEnd].kind == .removed { removedEnd += 1 }
            var addedEnd = removedEnd
            while addedEnd < lines.count, lines[addedEnd].kind == .added { addedEnd += 1 }
            var next = removedEnd
            for old in index..<removedEnd {
                guard next < addedEnd else { break }
                let candidates = (next..<addedEnd).prefix(pairWindow).map { ($0, marks(lines[old].text, lines[$0].text)) }
                guard let best = candidates.filter({ $0.1.similarity >= pairSimilarity }).max(by: { $0.1.similarity < $1.1.similarity }) else { continue }
                lines[old].marks = best.1.old
                lines[best.0].marks = best.1.new
                next = best.0 + 1
            }
            index = addedEnd
        }
        return lines
    }

    /// The words that differ between two lines, and the share of the longer line's visible characters they have in common.
    static func marks(_ old: String, _ new: String) -> (old: [Range<Int>], new: [Range<Int>], similarity: Double) {
        let visible = max(old.count { !$0.isWhitespace }, new.count { !$0.isWhitespace })
        guard old.count <= markLimit, new.count <= markLimit, visible > 0 else { return ([], [], 0) }
        let oldWords = words(old)
        let newWords = words(new)
        let difference = newWords.map(\.text).difference(from: oldWords.map(\.text))
        let removed = Set(difference.removals.map(\.offset))
        let inserted = Set(difference.insertions.map(\.offset))
        let kept = oldWords.indices.filter { !removed.contains($0) && !oldWords[$0].text.allSatisfy(\.isWhitespace) }
        let common = kept.reduce(0) { $0 + oldWords[$1].range.count }
        return (ranges(oldWords, removed), ranges(newWords, inserted), Double(common) / Double(visible))
    }

    private static func words(_ text: String) -> [(text: String, range: Range<Int>)] {
        var words: [(text: String, range: Range<Int>)] = []
        var current = ""
        var start = 0
        var offset = 0
        func kind(_ character: Character) -> Int {
            if character.isLetter || character.isNumber || character == "_" { return 0 }
            if character.isWhitespace { return 1 }
            return 2
        }
        for character in text {
            if let last = current.last, kind(last) != kind(character) || kind(character) == 2 {
                words.append((current, start..<offset))
                current = ""
                start = offset
            }
            current.append(character)
            offset += 1
        }
        if !current.isEmpty { words.append((current, start..<offset)) }
        return words
    }

    private static func ranges(_ words: [(text: String, range: Range<Int>)], _ changed: Set<Int>) -> [Range<Int>] {
        var ranges: [Range<Int>] = []
        for index in changed.sorted() {
            let range = words[index].range
            if let last = ranges.last, last.upperBound == range.lowerBound {
                ranges[ranges.count - 1] = last.lowerBound..<range.upperBound
            } else {
                ranges.append(range)
            }
        }
        return ranges
    }
}

/// One file's changes from tool calls: each edit, write or patch section is its own hunk.
struct FileDiff: Equatable {
    let path: String
    var hunks: [TextDiff]

    var change: FileChange {
        FileChange(path: path, added: hunks.reduce(0) { $0 + $1.added }, removed: hunks.reduce(0) { $0 + $1.removed })
    }

    /// Reads a patch in the `*** Begin Patch` format some agents send instead of edit arguments.
    static func patch(_ text: String) -> [FileDiff] {
        var files: [(path: String, hunks: [[TextDiff.Line]])] = []
        func append(_ kind: TextDiff.Kind, _ line: Substring) {
            if files[files.count - 1].hunks.isEmpty { files[files.count - 1].hunks.append([]) }
            files[files.count - 1].hunks[files[files.count - 1].hunks.count - 1].append(TextDiff.Line(kind: kind, text: String(line.dropFirst())))
        }
        for line in FileChange.lines(text) {
            if let path = ["*** Update File: ", "*** Add File: ", "*** Delete File: "].lazy.compactMap({ line.hasPrefix($0) ? String(line.dropFirst($0.count)) : nil }).first {
                files.append((path.trimmingCharacters(in: .whitespaces), []))
            } else if line.hasPrefix("*** ") || files.isEmpty {
                continue
            } else if line.hasPrefix("@@") {
                if files[files.count - 1].hunks.last?.isEmpty == false { files[files.count - 1].hunks.append([]) }
            } else if line.hasPrefix("+") {
                append(.added, line)
            } else if line.hasPrefix("-") {
                append(.removed, line)
            } else if line.hasPrefix(" ") {
                append(.same, line)
            }
        }
        return files.map { FileDiff(path: $0.path, hunks: $0.hunks.filter { !$0.isEmpty }.map(TextDiff.init(lines:))) }
    }

    /// One entry per file, in first-touched order, with the hunks of every call that changed it.
    static func merged(_ diffs: [FileDiff]) -> [FileDiff] {
        var merged: [FileDiff] = []
        for diff in diffs {
            if let index = merged.firstIndex(where: { $0.path == diff.path }) {
                merged[index].hunks += diff.hunks
            } else {
                merged.append(diff)
            }
        }
        return merged
    }
}

private extension CollectionDifference.Change {
    var offset: Int {
        switch self {
        case .insert(let offset, _, _), .remove(let offset, _, _): offset
        }
    }
}
