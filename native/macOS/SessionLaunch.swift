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

    init(_ receipt: SessionCreationReceipt, agent: String) {
        if receipt.unknown == true {
            self = .unconfirmed(receipt.id)
            return
        }
        guard receipt.ok || receipt.accepted == true else {
            if receipt.retryable == false, receipt.id != nil {
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
        if receipt.status == "submitted" && receipt.accepted == nil {
            self = .starting
            return
        }
        let target = receipt.launcher ?? agent
        let note = "Submitted to \(target). It appears in the sidebar once it starts."
        self = .submitted([note, receipt.warning].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: "\n"))
    }
}

struct SessionLaunchForm: Equatable {
    var agent = "claude"
    var folder = ""
    var model = ""
    var effort = ""
    var message = ""

    var usesFolder: Bool { agent != "notion" }
    var listsModels: Bool { agent != "notion" }

    var trimmedMessage: String { message.trimmingCharacters(in: .whitespacesAndNewlines) }
    var isComplete: Bool { !trimmedMessage.isEmpty && (!usesFolder || !folder.isEmpty) }

    mutating func switchAgent(to next: String) {
        guard next != agent else { return }
        self = SessionLaunchForm(agent: next, folder: folder, message: message)
    }

    mutating func selectModel(_ next: String) {
        guard next != model else { return }
        model = next
        effort = ""
    }

    var cwd: String { usesFolder ? folder : "" }
    var modelID: String { listsModels ? model : "" }

    func creationBody() throws -> Data {
        let effort = listsModels ? effort : ""
        let turnSettings = agent == "codex" && !effort.isEmpty ? TurnSettings(effort: effort) : TurnSettings()
        let claude = agent == "claude" ? ClaudeCreationSettings(effort: effort) : ClaudeCreationSettings()
        return try AgenthailAPI.creationBody(surface: agent, message: trimmedMessage, cwd: cwd, model: modelID, turnSettings: turnSettings, claude: claude)
    }

    func efforts(models: [ModelOption]) -> [String] {
        let selected = models.first { $0.id == model } ?? (model.isEmpty ? models.first { $0.default == true } : nil)
        return selected?.supportedReasoningEfforts ?? []
    }
}
