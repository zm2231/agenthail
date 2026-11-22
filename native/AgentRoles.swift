import Foundation

enum AgentDecision: String, CaseIterable, Codable, Sendable {
    case allow, notify, ask

    var title: String { rawValue.capitalized }
}

struct AgentRoleDefinition: Codable, Equatable, Sendable {
    var description: String?
    var reach: String
    var actions: [String: String]?
    var tools: [String]?
    var depth: Int?
    var running: Int?

    init(description: String? = nil, reach: String, actions: [String: String]? = nil, tools: [String]? = nil, depth: Int? = nil, running: Int? = nil) {
        self.description = description
        self.reach = reach
        self.actions = actions
        self.tools = tools
        self.depth = depth
        self.running = running
    }

    private enum CodingKeys: String, CodingKey { case description, reach, actions, tools, depth, running }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encodeIfPresent(description.flatMap { $0.isEmpty ? nil : $0 }, forKey: .description)
        try container.encode(reach, forKey: .reach)
        try container.encodeIfPresent(actions.flatMap { $0.isEmpty ? nil : $0 }, forKey: .actions)
        try container.encode(tools, forKey: .tools)
        try container.encodeIfPresent(depth.flatMap { $0 > 0 ? $0 : nil }, forKey: .depth)
        try container.encodeIfPresent(running.flatMap { $0 > 0 ? $0 : nil }, forKey: .running)
    }
}

struct AgentRoleView: Decodable, Identifiable, Equatable, Sendable {
    let name: String
    let builtIn: Bool
    let edited: Bool
    let description: String
    let reach: String
    let decisions: [String: String]
    let definition: AgentRoleDefinition
    let tools: [String]?
    let allTools: Bool
    let depth: Int
    let running: Int
    let effectiveDefaults: [String: String]?

    var id: String { name }
}

struct AgentActionView: Decodable, Identifiable, Equatable, Sendable {
    let name: String
    let `default`: String
    let detail: String
    let ownProject: Bool

    var id: String { name }
}

struct AgentRoleAssignment: Decodable, Identifiable, Equatable, Sendable {
    let kind: String
    let target: String
    let role: String
    let stale: String?

    var id: String { kind + "\u{0}" + target }
}

struct AgentRolesView: Decodable, Equatable, Sendable {
    var roles: [AgentRoleView]
    var assignments: [AgentRoleAssignment]
    var actions: [AgentActionView]
    var tools: [String]

    func role(_ name: String) -> AgentRoleView? { roles.first { $0.name == name } }
}

struct SessionRole: Decodable, Equatable, Sendable {
    let name: String
    let reach: String?
    let source: String?
    let replaces: String?
    let label: String
}

enum AgentAssignmentKind: String, CaseIterable, Identifiable, Sendable {
    case session, identity, alias

    var id: String { rawValue }
    var title: String { rawValue.capitalized }
}

struct AgentRoleRequest: Encodable, Equatable, Sendable {
    let op: String
    let name: String
    var role: AgentRoleDefinition?
    var kind: String?
    var target: String?

    static func put(_ name: String, _ role: AgentRoleDefinition) -> Self { Self(op: "put", name: name, role: role) }
    static func delete(_ name: String) -> Self { Self(op: "delete", name: name) }
    static func assign(_ name: String, kind: AgentAssignmentKind, target: String) -> Self { Self(op: "assign", name: name, kind: kind.rawValue, target: target) }
    static func unassign(_ assignment: AgentRoleAssignment) -> Self { Self(op: "unassign", name: assignment.role, kind: assignment.kind, target: assignment.target) }
}

enum AgentRoleFacts {
    static let reaches: [(id: String, title: String)] = [("descendants", "Sessions it started"), ("project", "Its project"), ("all", "Every session")]
    static let defaultDepth = 3
    static let defaultRunning = 8

    static func reachTitle(_ reach: String) -> String { reaches.first { $0.id == reach }?.title ?? reach }

    static func effectiveDefault(_ action: AgentActionView, in role: AgentRoleView?) -> String {
        role?.effectiveDefaults?[action.name] ?? action.default
    }

    static func nameHint(_ name: String, taken: [String]) -> String? {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { return nil }
        if taken.contains(trimmed) { return "A role named \(trimmed) exists." }
        if trimmed.range(of: "^[a-z][a-z0-9-]{0,39}$", options: .regularExpression) == nil {
            return "Lowercase letters, digits and dashes, starting with a letter."
        }
        return nil
    }

    static func assignmentLine(_ assignment: AgentRoleAssignment, sessionName: (String) -> String?) -> String {
        switch assignment.kind {
        case "session": return sessionName(assignment.target) ?? assignment.target
        default: return "@" + assignment.target
        }
    }
}

struct AgentRoleDraft: Equatable, Sendable {
    let name: String
    private(set) var base: AgentRoleDefinition
    var definition: AgentRoleDefinition

    init(_ role: AgentRoleView) {
        name = role.name
        base = role.definition
        definition = role.definition
    }

    init(name: String, copying definition: AgentRoleDefinition) {
        self.name = name
        base = definition
        self.definition = definition
    }

    var changed: Bool { definition != base }

    func decision(_ action: String) -> AgentDecision? {
        definition.actions?[action].flatMap(AgentDecision.init(rawValue:))
    }

    mutating func setDecision(_ decision: AgentDecision?, for action: AgentActionView, effectiveDefault: String) {
        var actions = definition.actions ?? [:]
        if let decision, decision.rawValue != effectiveDefault {
            actions[action.name] = decision.rawValue
        } else {
            actions[action.name] = nil
        }
        definition.actions = actions.isEmpty ? nil : actions
    }

    var depth: Int {
        get { definition.depth.flatMap { $0 > 0 ? $0 : nil } ?? AgentRoleFacts.defaultDepth }
        set { definition.depth = newValue == AgentRoleFacts.defaultDepth ? nil : newValue }
    }

    var running: Int {
        get { definition.running.flatMap { $0 > 0 ? $0 : nil } ?? AgentRoleFacts.defaultRunning }
        set { definition.running = newValue == AgentRoleFacts.defaultRunning ? nil : newValue }
    }

    var allTools: Bool { definition.tools == nil }

    func sees(_ tool: String) -> Bool { definition.tools?.contains(tool) ?? true }

    mutating func setAllTools(_ all: Bool, every: [String]) {
        definition.tools = all ? nil : every
    }

    mutating func setTool(_ tool: String, shown: Bool, every: [String]) {
        var tools = Set(definition.tools ?? every)
        if shown { tools.insert(tool) } else { tools.remove(tool) }
        definition.tools = tools.count == every.count && Set(every) == tools ? nil : every.filter(tools.contains)
    }

    mutating func revert() { definition = base }

    var request: AgentRoleRequest { .put(name, definition) }
}

enum AgentRolesRefresh {
    static let source = "agent-roles"

    static func refetches(type: String, source: String?) -> Bool {
        type == "settings.updated" && source == Self.source
    }
}
