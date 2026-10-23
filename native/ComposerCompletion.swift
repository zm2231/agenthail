import Foundation

struct SessionFile: Decodable, Equatable, Sendable {
    let path: String
    let dir: Bool
}

struct SessionFiles: Decodable, Equatable, Sendable {
    let files: [SessionFile]
    let truncated: Bool
}

struct CompletionToken: Equatable {
    enum Trigger: UInt16 {
        case mention = 0x40
        case command = 0x2F
        case skill = 0x24
    }

    let trigger: Trigger
    let query: String
    let range: NSRange
    let atStart: Bool

    static func at(_ text: String, caret: Int) -> CompletionToken? {
        let string = text as NSString
        guard caret >= 0, caret <= string.length else { return nil }
        var start = caret
        while start > 0, !isSpace(string.character(at: start - 1)) { start -= 1 }
        guard start < caret, let trigger = Trigger(rawValue: string.character(at: start)) else { return nil }
        var end = caret
        while end < string.length, !isSpace(string.character(at: end)) { end += 1 }
        let query = string.substring(with: NSRange(location: start + 1, length: caret - start - 1))
        let atStart = string.substring(to: start).allSatisfy(\.isWhitespace)
        return CompletionToken(trigger: trigger, query: query, range: NSRange(location: start, length: end - start), atStart: atStart)
    }

    private static func isSpace(_ unit: unichar) -> Bool {
        guard let scalar = Unicode.Scalar(unit) else { return false }
        return CharacterSet.whitespacesAndNewlines.contains(scalar)
    }
}

struct CompletionItem: Identifiable, Equatable {
    let id: String
    let title: String
    let detail: String
    let insertion: String
}

enum ComposerCompletion {
    /// `appCommands` are the app's own actions, which act on the whole
    /// message and so are offered only at its start.
    static func items(for token: CompletionToken, appCommands: [CompletionItem], runtime: RuntimeCommands?, sessions: [CompletionItem], files: [SessionFile]) -> [CompletionItem] {
        switch token.trigger {
        case .command:
            let app = token.atStart ? matching(appCommands, token.query) { String($0.title.dropFirst()) } : []
            return app + matching(runtimeItems(runtime, prefix: "/"), token.query) { $0.title }
        case .skill:
            return matching(runtimeItems(runtime, prefix: "$"), token.query) { $0.title }
        case .mention:
            let mentioned = Array(matching(sessions, token.query) { String($0.title.dropFirst()) }.prefix(mentionedSessionLimit))
            return mentioned + files.map { CompletionItem(id: "file:" + $0.path, title: $0.path + ($0.dir ? "/" : ""), detail: $0.dir ? "Folder" : "", insertion: "@" + $0.path + ($0.dir ? "/" : "")) }
        }
    }

    static func notice(for token: CompletionToken, runtime: RuntimeCommands?) -> String? {
        guard token.trigger == .command, let reason = runtime?.unavailable, !reason.isEmpty else { return nil }
        return reason
    }

    /// The token's replacement and where the caret goes after it: past one
    /// space, the word's own when one follows. A folder keeps the word open so
    /// its contents can be completed next.
    static func edit(_ item: CompletionItem, in text: String, at token: CompletionToken) -> (range: NSRange, replacement: String, caret: Int) {
        let string = text as NSString
        let end = NSMaxRange(token.range)
        let spaced = end < string.length && string.substring(with: NSRange(location: end, length: 1)).allSatisfy(\.isWhitespace)
        let open = item.insertion.hasSuffix("/") || item.insertion.hasSuffix(" ")
        let replacement = open || spaced ? item.insertion : item.insertion + " "
        let caret = token.range.location + (replacement as NSString).length + (spaced && !open ? 1 : 0)
        return (token.range, replacement, caret)
    }

    static func apply(_ item: CompletionItem, to text: String, at token: CompletionToken) -> (text: String, caret: Int) {
        let change = edit(item, in: text, at: token)
        return ((text as NSString).replacingCharacters(in: change.range, with: change.replacement), change.caret)
    }

    static let mentionedSessionLimit = 8

    /// Sessions a message can name with `@`: open or named ones on the same
    /// computer, by alias or by the id that computer knows them by.
    static func sessions(_ sessions: [SessionState], from current: SessionState) -> [CompletionItem] {
        let host = hostPrefix(current.id)
        return sessions.compactMap { session in
            let alias = session.alias.flatMap { $0.isEmpty ? nil : $0 }
            guard session.id != current.id, hostPrefix(session.id) == host, !session.isReadOnly, alias != nil || session.open else { return nil }
            let handle = alias ?? localID(session.id)
            let detail = [session.name, session.surface].filter { !$0.isEmpty && $0 != handle }.joined(separator: " · ")
            return CompletionItem(id: "session:" + session.id, title: "@" + handle, detail: detail, insertion: "@" + handle)
        }
    }

    private static func runtimeItems(_ runtime: RuntimeCommands?, prefix: String) -> [CompletionItem] {
        (runtime?.commands ?? []).filter { $0.insert.hasPrefix(prefix) }.map {
            CompletionItem(id: "runtime:" + $0.insert, title: $0.insert + ($0.argumentHint.isEmpty ? "" : " " + $0.argumentHint), detail: $0.description, insertion: $0.insert)
        }
    }

    private static func matching(_ items: [CompletionItem], _ query: String, key: (CompletionItem) -> String) -> [CompletionItem] {
        guard !query.isEmpty else { return items }
        let prefixed = items.filter { key($0).range(of: query, options: [.caseInsensitive, .anchored]) != nil }
        let contained = items.filter { key($0).range(of: query, options: .caseInsensitive) != nil && !prefixed.contains($0) }
        return prefixed + contained
    }

    private static func hostPrefix(_ id: String) -> String {
        id.range(of: "::").map { String(id[..<$0.lowerBound]) } ?? ""
    }

    private static func localID(_ id: String) -> String {
        id.range(of: "::").map { String(id[$0.upperBound...]) } ?? id
    }
}
