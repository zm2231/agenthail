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
