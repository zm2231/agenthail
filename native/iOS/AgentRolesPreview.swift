import Foundation

enum AgentRolesPreview {
    nonisolated static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--preview-roles") }
    nonisolated(unsafe) private static var custom: [String: [String: Any]] = ["reviewer": ["description": "Reviews changes across the project.", "reach": "project", "actions": ["project-archive": "ask"], "tools": ["agenthail_read", "agenthail_send"]]]
    nonisolated(unsafe) private static var edited: [String: [String: Any]] = [:]
    nonisolated(unsafe) private static var assignments: [[String: Any]] = [
        ["kind": "alias", "target": "reviewer", "role": "reviewer"],
        ["kind": "session", "target": "gone-session", "role": "lead", "stale": "no such session"],
    ]

    nonisolated(unsafe) private static let actions: [[String: Any]] = [
        ["name": "session-create-remote", "default": "notify", "detail": "Start a session on a paired computer", "ownProject": false],
        ["name": "project-archive", "default": "notify", "detail": "Archive its own project", "ownProject": true],
        ["name": "coordinator-set", "default": "allow", "detail": "Make a session in its own project the lead", "ownProject": true],
        ["name": "cross-project-steer", "default": "notify", "detail": "Steer a session in another project on this computer", "ownProject": false],
    ]
    nonisolated private static let tools = ["agenthail_create", "agenthail_read", "agenthail_send", "agenthail_steer", "agenthail_wait"]
    nonisolated private static let builtIn: [(String, String, String)] = [
        ("worker", "descendants", "Any session without another role: it acts on the sessions it started."),
        ("lead", "project", "A project's coordinator: it manages its own project and its sessions."),
        ("operator", "all", "The person's voice operator: it acts for the person everywhere."),
        ("remote", "all", "An agent identity that reaches this computer from elsewhere: it acts for the person everywhere."),
    ]

    nonisolated(unsafe) static let sessionRole: [String: Any] = ["name": "reviewer", "reach": "project", "source": "assigned to this session", "replaces": "worker", "label": "worker (replaced by reviewer)"]

    nonisolated static func respond(_ method: String?, _ body: [String: Any]) -> (status: Int, body: String) {
        if method == "POST" {
            let name = body["name"] as? String ?? ""
            switch body["op"] as? String {
            case "put":
                guard name.range(of: "^[a-z][a-z0-9-]{0,39}$", options: .regularExpression) != nil || builtIn.contains(where: { $0.0 == name }) else {
                    return (400, #"{"error":{"code":"invalid_request","message":"a role's name is lowercase letters, digits and dashes, starting with a letter, at most 40 long"}}"#)
                }
                let role = body["role"] as? [String: Any] ?? [:]
                if builtIn.contains(where: { $0.0 == name }) { edited[name] = role } else { custom[name] = role }
            case "delete":
                edited[name] = nil
                custom[name] = nil
            case "assign":
                let kind = body["kind"] as? String ?? "", target = (body["target"] as? String ?? "").replacingOccurrences(of: "@", with: "")
                assignments.removeAll { $0["kind"] as? String == kind && $0["target"] as? String == target }
                assignments.append(["kind": kind, "target": target, "role": name])
            case "unassign":
                assignments.removeAll { $0["kind"] as? String == body["kind"] as? String && $0["target"] as? String == body["target"] as? String }
            default:
                return (400, #"{"error":{"code":"invalid_request","message":"op is put, delete, assign or unassign"}}"#)
            }
        }
        return (200, viewJSON())
    }

    nonisolated private static func viewJSON() -> String {
        var roles: [[String: Any]] = builtIn.map { name, reach, description in
            role(name, builtIn: true, edited: edited[name] != nil, definition: edited[name] ?? ["description": description, "reach": reach])
        }
        roles += custom.keys.sorted().map { role($0, builtIn: false, edited: false, definition: custom[$0]!) }
        let view: [String: Any] = ["roles": roles, "assignments": assignments, "actions": actions, "tools": tools]
        return String(decoding: try! JSONSerialization.data(withJSONObject: view), as: UTF8.self)
    }

    nonisolated private static func role(_ name: String, builtIn: Bool, edited: Bool, definition: [String: Any]) -> [String: Any] {
        let reach = definition["reach"] as? String ?? "descendants"
        let overrides = definition["actions"] as? [String: String] ?? [:]
        var defaults: [String: String] = [:]
        for action in actions {
            let name = action["name"] as! String
            defaults[name] = action["ownProject"] as! Bool && reach == "descendants" ? "ask" : action["default"] as! String
        }
        let decisions = defaults.merging(overrides) { _, override in override }
        let shown = definition["tools"] as? [String]
        return ["name": name, "builtIn": builtIn, "edited": edited, "description": definition["description"] as? String ?? "", "reach": reach,
                "decisions": decisions, "definition": definition, "tools": shown ?? tools, "allTools": shown == nil,
                "depth": definition["depth"] as? Int ?? 3, "running": definition["running"] as? Int ?? 8, "effectiveDefaults": defaults]
    }
}
