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
                if name == "TaskUpdate", let id = input["taskId"] as? String {
                    return (["Task \(id)"] + [(input["status"] as? String)?.replacingOccurrences(of: "_", with: " ")].compactMap { $0 }).joined(separator: " · ")
                }
                if name == "TaskCreate", let subject = input["subject"] as? String, !subject.isEmpty { return subject }
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
            case "TaskCreate": return "Add task"
            case "TaskUpdate": return "Update task"
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
            case "TaskCreate", "TaskUpdate": return "checklist"
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
        if !["TaskCreate", "TaskUpdate"].contains(name), let description = input["description"] as? String, !description.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
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

    /// The text's lines; a final newline ends the last line rather than starting another.
    static func lines(_ text: String) -> [Substring] {
        var lines = text.split(separator: "\n", omittingEmptySubsequences: false)
        if lines.last?.isEmpty == true { lines.removeLast() }
        return lines
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
    var changes: [FileChange] { diffs.map(\.change) }

    /// What the call changed in each file, for edits, writes and patches.
    var diffs: [FileDiff] {
        switch content {
        case .edit(let path, let before, let after):
            return [FileDiff(path: path, hunks: [TextDiff(before: before, after: after)])]
        case .raw:
            let input = Self.input(rawText)
            let path = input?["file_path"] as? String ?? input?["path"] as? String
            switch name {
            case "Write", "write_file":
                guard let path, let content = input?["content"] as? String else { return [] }
                return [FileDiff(path: path, hunks: [TextDiff(before: "", after: content)])]
            case "MultiEdit":
                guard let path, let edits = input?["edits"] as? [[String: Any]] else { return [] }
                return [FileDiff(path: path, hunks: edits.map { TextDiff(before: $0["old_string"] as? String ?? "", after: $0["new_string"] as? String ?? "") })]
            case "apply_patch":
                let patch = input?["input"] as? String ?? input?["patch"] as? String ?? rawText
                return FileDiff.patch(patch)
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
