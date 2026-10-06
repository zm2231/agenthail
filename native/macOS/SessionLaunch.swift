import Foundation

enum SessionLaunchOutcome: Equatable {
    case opened
    case submitted(String)
    case starting(String)
    case halted(String)
    case uncertain(String)
    case failed(String)

    var keepsRetryKey: Bool {
        switch self {
        case .starting, .halted, .uncertain: return true
        case .opened, .submitted, .failed: return false
        }
    }
}

struct SessionLaunchKey {
    private var pending: (body: Data, key: String)?

    mutating func key(for body: Data) -> String {
        if let pending, pending.body == body { return pending.key }
        let key = UUID().uuidString
        pending = (body, key)
        return key
    }

    mutating func record(_ outcome: SessionLaunchOutcome) {
        if !outcome.keepsRetryKey { pending = nil }
    }
}

enum SessionLaunchDecision: Equatable {
    case open(String)
    case submitted(String)
    case starting
    case unconfirmed(String?)
    case halted(String)
    case failed(String)

    init(_ receipt: SessionCreationReceipt, launcher: String?, agent: String) {
        if receipt.unknown == true {
            self = .unconfirmed(receipt.id)
            return
        }
        guard receipt.ok || receipt.accepted == true else {
            if receipt.retryable == false {
                self = .halted(receipt.error ?? "The session didn't start.")
            } else {
                self = .failed(receipt.error ?? "The session didn't start.")
            }
            return
        }
        if let id = receipt.id, !id.isEmpty {
            self = .open(id)
            return
        }
        if receipt.accepted == nil {
            self = .starting
            return
        }
        let target = receipt.launcher ?? launcher ?? agent
        let note = "Submitted to \(target). It appears in the sidebar once it starts."
        self = .submitted([note, receipt.warning].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: "\n"))
    }
}

struct SessionLaunchForm: Equatable {
    static let codexEfforts = ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"]
    static let claudeEfforts = ["low", "medium", "high", "xhigh", "max"]

    var agent = "claude"
    var launcher: String?
    var alias = ""
    var folder = ""
    var model = ""
    var message = ""
    var effort = ""
    var mode: TurnSettings.Mode?
    var codex = CodexCreationSettings()
    var claudeAgent = ""
    var worktree = ""
    var permissionMode = ""

    var usesFolder: Bool { agent != "notion" }
    var listsModels: Bool { agent != "notion" }
    var hasOptions: Bool { (agent == "codex" || agent == "claude") && launcher == nil }

    var trimmedMessage: String { message.trimmingCharacters(in: .whitespacesAndNewlines) }
    var handle: String { SessionHandle.normalized(alias) }

    var problem: String? {
        if !handle.isEmpty, let problem = SessionHandle.problem(with: alias) { return problem }
        if agent == "codex" && launcher == nil { return codex.schemaProblem }
        return nil
    }

    var isComplete: Bool { problem == nil && !trimmedMessage.isEmpty && (!usesFolder || !folder.isEmpty) }

    mutating func switchAgent(to next: String) {
        guard next != agent else { return }
        self = SessionLaunchForm(agent: next, alias: alias, folder: folder, message: message)
    }

    mutating func selectLauncher(_ next: String?) {
        launcher = next
        guard next != nil else { return }
        clearOptions()
    }

    mutating func selectModel(_ next: String) {
        guard next != model else { return }
        model = next
        effort = ""
    }

    mutating func clearOptions() {
        effort = ""
        mode = nil
        codex = .init()
        claudeAgent = ""
        worktree = ""
        permissionMode = ""
    }

    var turnSettings: TurnSettings {
        guard agent == "codex", launcher == nil else { return .init() }
        return TurnSettings(effort: effort.isEmpty ? nil : effort, mode: mode)
    }

    var codexSettings: CodexCreationSettings {
        agent == "codex" && launcher == nil ? codex : .init()
    }

    var claudeSettings: ClaudeCreationSettings {
        guard agent == "claude", launcher == nil else { return .init() }
        return ClaudeCreationSettings(worktree: worktree.trimmingCharacters(in: .whitespacesAndNewlines), agent: claudeAgent.trimmingCharacters(in: .whitespacesAndNewlines), effort: effort, permissionMode: permissionMode)
    }

    var cwd: String { usesFolder ? folder : "" }
    var modelID: String { listsModels ? model.trimmingCharacters(in: .whitespacesAndNewlines) : "" }

    func creationBody() throws -> Data {
        try AgenthailAPI.creationBody(surface: agent, message: trimmedMessage, cwd: cwd, model: modelID, alias: handle, turnSettings: turnSettings, codex: codexSettings, claude: claudeSettings, launcher: launcher)
    }

    func efforts(models: [ModelOption]) -> [String] {
        let accepted = agent == "codex" ? Self.codexEfforts : agent == "claude" ? Self.claudeEfforts : []
        let selected = models.first { $0.id == model } ?? (model.isEmpty ? models.first { $0.default == true } : nil)
        return (selected?.supportedReasoningEfforts ?? accepted).filter(accepted.contains)
    }
}
