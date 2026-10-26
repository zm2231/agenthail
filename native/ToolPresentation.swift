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
    let text: String
    var intent: String? = nil
    var summary: String {
        switch content {
        case .command(let command, _): return command
        case .edit(let path, _, _): return path
        case .plan(let steps): return steps.map { $0.0 }.joined(separator: " · ")
        case .raw(let text):
            guard let input = Self.input(text) else { return text.isEmpty ? "No input parameters" : text }
            if name == "TaskUpdate", let id = input["taskId"] as? String {
                return (["Task \(id)"] + [(input["status"] as? String)?.replacingOccurrences(of: "_", with: " ")].compactMap { $0 }).joined(separator: " · ")
            }
            if name == "TaskCreate", let subject = input["subject"] as? String, !subject.isEmpty { return subject }
            for key in Self.summaryKeys + input.keys.sorted() {
                if let value = input[key] as? String, !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return value }
            }
            switch input.count {
            case 0: return "No input parameters"
            case 1: return "1 field"
            default: return "\(input.count) fields"
            }
        }
    }

    /// Fields that name what a call acted on, most telling first; any other text field comes after them.
    private static let summaryKeys = ["description", "file_path", "path", "pattern", "query", "url", "prompt", "subject", "message", "title", "name"]

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
        self.text = text
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
        var lines = text.split(omittingEmptySubsequences: false) { $0 == "\n" || $0 == "\r\n" }
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
        ToolDiffCache.diffs(name: name, text: text) { madeDiffs }
    }

    private var madeDiffs: [FileDiff] {
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
        if case .raw = content { return text }
        return ""
    }

    /// The recorded input as a person reads it: a command first, then every other field as `key: value`,
    /// with nested values as indented JSON. Input that is not a JSON object reads as recorded.
    var readableInput: String {
        guard let input = Self.input(text), !input.isEmpty else { return text }
        let commandKey = ["command", "cmd"].first { input[$0] is String }
        var lines = commandKey.map { ["$ " + (input[$0] as! String)] } ?? []
        for key in input.keys.sorted() where key != commandKey {
            lines.append(key + ": " + Self.readable(input[key]!))
        }
        return lines.joined(separator: "\n")
    }

    private static func readable(_ value: Any) -> String {
        if let string = value as? String { return string }
        return json(value, indent: "")
    }

    /// JSON with sorted keys and two-space indents, written out here so the dashboard can match it exactly.
    private static func json(_ value: Any, indent: String) -> String {
        let inner = indent + "  "
        switch value {
        case let string as String:
            return quoted(string)
        case let number as NSNumber:
            if CFGetTypeID(number) == CFBooleanGetTypeID() { return number.boolValue ? "true" : "false" }
            return number.stringValue
        case let array as [Any]:
            if array.isEmpty { return "[]" }
            return "[\n" + array.map { inner + json($0, indent: inner) }.joined(separator: ",\n") + "\n" + indent + "]"
        case let object as [String: Any]:
            if object.isEmpty { return "{}" }
            return "{\n" + object.keys.sorted().map { inner + quoted($0) + ": " + json(object[$0]!, indent: inner) }.joined(separator: ",\n") + "\n" + indent + "}"
        default:
            return "null"
        }
    }

    private static func quoted(_ string: String) -> String {
        var out = "\""
        for scalar in string.unicodeScalars {
            switch scalar {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\n": out += "\\n"
            case "\r": out += "\\r"
            case "\t": out += "\\t"
            case "\u{08}": out += "\\b"
            case "\u{0C}": out += "\\f"
            case _ where scalar.value < 0x20: out += String(format: "\\u%04x", scalar.value)
            default: out.unicodeScalars.append(scalar)
            }
        }
        return out + "\""
    }

    private static func input(_ text: String) -> [String: Any]? {
        guard let data = text.data(using: .utf8) else { return nil }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
    }

    private static func firstLine(_ text: String) -> String {
        text.split(omittingEmptySubsequences: true) { $0 == "\n" || $0 == "\r\n" }.first.map(String.init) ?? ""
    }
}

/// What became of a call shown without its result, read from the timeline after it: its result came later
/// apart from it, or the turn that made it ended, was interrupted or failed, or it is still going. Never a
/// guess: a call whose turn has no recorded end reads as running only while its session is active.
enum ToolCallState: String {
    case running, interrupted, failed, unrecorded, later

    var label: String {
        switch self {
        case .running: return "Running"
        case .interrupted: return "Interrupted before a result"
        case .failed: return "The turn failed before a result"
        case .unrecorded: return "No result recorded"
        case .later: return "Result recorded further down"
        }
    }
}

/// The state of every call in a timeline, by item id.
struct ToolCallStates: Equatable {
    private var states: [String: ToolCallState] = [:]

    init() {}

    /// `active` is whether the session is working or waiting on someone now.
    init(items: [TimelineItem], active: Bool) {
        var open: [TimelineItem] = []
        func end(_ state: ToolCallState, where ends: (TimelineItem) -> Bool) {
            for call in open where ends(call) { states[call.id] = state }
            open.removeAll(where: ends)
        }
        for item in items {
            if ToolRunSummary.isCall(item) {
                open.append(item)
            } else if item.kind == "toolResult", let call = item.callId {
                end(.later) { $0.callId == call }
            } else if item.kind == "done" || (item.kind == "event" && item.title == "Turn interrupted") {
                let state = item.kind == "event" ? .interrupted : Self.state(ending: item.status ?? "")
                end(state) { $0.turnId == nil || item.turnId == nil || $0.turnId == item.turnId }
            } else if let turn = item.turnId {
                end(.unrecorded) { $0.turnId != nil && $0.turnId != turn }
            }
        }
        for call in open { states[call.id] = active ? .running : .unrecorded }
    }

    subscript(call: String) -> ToolCallState { states[call] ?? .unrecorded }

    private static func state(ending status: String) -> ToolCallState {
        switch status {
        case "cancelled", "canceled", "interrupted", "task_cancelled", "task_canceled", "task_aborted", "turn_aborted": return .interrupted
        case "failed", "error", "systemerror": return .failed
        default: return .unrecorded
        }
    }
}
