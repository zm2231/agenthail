import Foundation

struct ToolPresentation {
    enum Content {
        case command(String, String?)
        case edit(String, String, String)
        case plan([(String, String)])
        case raw(String)
    }
    let content: Content
    var summary: String {
        switch content {
        case .command(let command, _): return command
        case .edit(let path, _, _): return path
        case .plan(let steps): return steps.map { $0.0 }.joined(separator: " · ")
        case .raw(let text): return text
        }
    }

    init(name: String, text: String) {
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
