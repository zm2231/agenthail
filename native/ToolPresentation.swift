import Foundation

struct ToolPresentation {
    enum Content {
        case command(String, String?)
        case edit(String, String, String)
        case plan([(String, String)])
        case raw(String)
    }
    let content: Content
    let name: String
    var intent: String? = nil
    var summary: String {
        switch content {
        case .command(let command, _): return command
        case .edit(let path, _, _): return path
        case .plan(let steps): return steps.map { $0.0 }.joined(separator: " · ")
        case .raw(let text):
            if let data = text.data(using: .utf8), let input = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                for key in ["description", "file_path", "path", "pattern", "query", "url", "prompt"] {
                    if let value = input[key] as? String, !value.isEmpty { return value }
                }
            }
            return text.isEmpty ? "No input parameters" : text
        }
    }

    var title: String {
        switch content {
        case .command: return "Run command"
        case .edit: return "Edit file"
        case .plan: return "Update plan"
        case .raw:
            switch name {
            case "Read", "read_file": return "Read file"
            case "Write", "write_file": return "Write file"
            case "Grep", "Glob": return "Search files"
            case "WebSearch": return "Search the web"
            case "WebFetch": return "Open webpage"
            case "Agent", "Task": return "Delegate to agent"
            default:
                return name.replacingOccurrences(of: "functions.", with: "")
                    .replacingOccurrences(of: "mcp__", with: "")
                    .replacingOccurrences(of: "__", with: " · ")
                    .replacingOccurrences(of: "_", with: " ")
            }
        }
    }

    var symbol: String {
        switch content {
        case .command: return "terminal"
        case .edit: return "pencil.line"
        case .plan: return "checklist"
        case .raw:
            switch name {
            case "Read", "read_file": return "doc.text.magnifyingglass"
            case "Write", "write_file": return "doc.badge.plus"
            case "Grep", "Glob", "WebSearch": return "magnifyingglass"
            case "WebFetch": return "globe"
            case "Agent", "Task": return "person.2"
            default: return "wrench.and.screwdriver"
            }
        }
    }

    init(name: String, text: String) {
        self.name = name
        guard let data = text.data(using: .utf8),
              let input = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            content = .raw(text)
            return
        }
        if let description = input["description"] as? String, !description.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            intent = description
        }
        if let command = input["command"] as? String ?? input["cmd"] as? String {
            content = .command(command, input["workdir"] as? String ?? input["cwd"] as? String)
        } else if let path = input["file_path"] as? String ?? input["path"] as? String,
                  let before = input["old_string"] as? String ?? input["oldText"] as? String,
                  let after = input["new_string"] as? String ?? input["newText"] as? String {
            content = .edit(path, before, after)
        } else if name == "TodoWrite" || name.hasSuffix("update_plan"),
                  let steps = input["todos"] as? [[String: Any]] ?? input["plan"] as? [[String: Any]] {
            content = .plan(steps.map { ($0["content"] as? String ?? $0["step"] as? String ?? "", $0["status"] as? String ?? "") })
        } else {
            content = .raw(text)
        }
    }
}

/// Lines a tool call added to and removed from one file.
struct FileChange: Equatable {
    let path: String
    var added: Int
    var removed: Int

    var name: String { (path as NSString).lastPathComponent }

    /// One entry per file, in first-touched order, summing every call that changed it.
    static func merged(_ changes: [FileChange]) -> [FileChange] {
        var merged: [FileChange] = []
        for change in changes {
            if let index = merged.firstIndex(where: { $0.path == change.path }) {
                merged[index].added += change.added
                merged[index].removed += change.removed
            } else {
                merged.append(change)
            }
        }
        return merged
    }

    static func lineCounts(before: String, after: String) -> (added: Int, removed: Int) {
        let difference = lines(after).difference(from: lines(before))
        return (difference.insertions.count, difference.removals.count)
    }

    static func lines(_ text: String) -> [Substring] {
        text.isEmpty ? [] : text.split(separator: "\n", omittingEmptySubsequences: false)
    }

    /// Reads a patch in the `*** Begin Patch` format some agents send instead of edit arguments.
    static func patch(_ text: String) -> [FileChange] {
        var changes: [FileChange] = []
        for line in text.split(separator: "\n", omittingEmptySubsequences: false) {
            if let path = ["*** Update File: ", "*** Add File: ", "*** Delete File: "].lazy.compactMap({ line.hasPrefix($0) ? String(line.dropFirst($0.count)) : nil }).first {
                changes.append(FileChange(path: path.trimmingCharacters(in: .whitespaces), added: 0, removed: 0))
            } else if line.hasPrefix("*** ") {
                continue
            } else if line.hasPrefix("+"), !changes.isEmpty {
                changes[changes.count - 1].added += 1
            } else if line.hasPrefix("-"), !changes.isEmpty {
                changes[changes.count - 1].removed += 1
            }
        }
        return changes
    }
}

extension ToolPresentation {
    /// What the call did, as a verb for a one-line transcript entry.
    var verb: String {
        switch content {
        case .command: return "Run"
        case .edit: return "Edit"
        case .plan: return "Update plan"
        case .raw:
            switch name {
            case "Read", "read_file", "NotebookRead", "view_image": return "Read"
            case "Write", "write_file": return "Write"
            case "MultiEdit", "NotebookEdit", "apply_patch": return "Edit"
            case "Grep", "Glob", "LS", "ToolSearch": return "Search"
            case "WebSearch", "web_search": return "Search the web"
            case "WebFetch": return "Open"
            case "Agent", "Task", "spawn_agent": return "Delegate"
            default: return title
            }
        }
    }

    /// What the call acted on: a command, a file name or a query. Commands and paths read as code.
    var object: (text: String, isCode: Bool) {
        switch content {
        case .command(let command, _):
            if let intent { return (intent, false) }
            return (Self.firstLine(command), true)
        case .edit(let path, _, _):
            return ((path as NSString).lastPathComponent, true)
        case .plan:
            return ("", false)
        case .raw:
            if name == "apply_patch" { return (changes.map(\.name).joined(separator: ", "), true) }
            let input = Self.input(rawText)
            if let pattern = input?["pattern"] as? String { return (pattern, true) }
            if let path = input?["file_path"] as? String ?? input?["path"] as? String ?? input?["notebook_path"] as? String {
                return ((path as NSString).lastPathComponent, true)
            }
            let text = Self.firstLine(summary)
            return (text == "No input parameters" ? "" : text, false)
        }
    }

    /// Files the call changed, with line counts, for edits, writes and patches.
    var changes: [FileChange] {
        switch content {
        case .edit(let path, let before, let after):
            let counts = FileChange.lineCounts(before: before, after: after)
            return [FileChange(path: path, added: counts.added, removed: counts.removed)]
        case .raw:
            let input = Self.input(rawText)
            let path = input?["file_path"] as? String ?? input?["path"] as? String
            switch name {
            case "Write", "write_file":
                guard let path, let content = input?["content"] as? String else { return [] }
                return [FileChange(path: path, added: FileChange.lines(content).count, removed: 0)]
            case "MultiEdit":
                guard let path, let edits = input?["edits"] as? [[String: Any]] else { return [] }
                let counts = edits.map { FileChange.lineCounts(before: $0["old_string"] as? String ?? "", after: $0["new_string"] as? String ?? "") }
                return [FileChange(path: path, added: counts.reduce(0) { $0 + $1.added }, removed: counts.reduce(0) { $0 + $1.removed })]
            case "apply_patch":
                let patch = input?["input"] as? String ?? input?["patch"] as? String ?? rawText
                return FileChange.patch(patch)
            default:
                return []
            }
        default:
            return []
        }
    }

    private var rawText: String {
        if case .raw(let text) = content { return text }
        return ""
    }

    private static func input(_ text: String) -> [String: Any]? {
        guard let data = text.data(using: .utf8) else { return nil }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
    }

    private static func firstLine(_ text: String) -> String {
        text.split(separator: "\n", omittingEmptySubsequences: true).first.map(String.init) ?? ""
    }
}
