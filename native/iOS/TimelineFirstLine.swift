import Foundation

extension TimelineItem {
    static func firstLine(_ text: String) -> String {
        var rest = text[...]
        while let newline = rest.firstIndex(of: "\n") {
            if newline != rest.startIndex { return String(rest[..<newline]) }
            rest = rest[rest.index(after: newline)...]
        }
        return String(rest)
    }
}
