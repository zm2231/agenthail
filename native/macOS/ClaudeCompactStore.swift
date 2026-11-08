import Foundation
import Observation

@MainActor
@Observable
final class ClaudeCompactStore {
    private(set) var roots: [ClaudeCompactRoot]?
    private(set) var holds: [ClaudeCompactHold] = []
    private(set) var loadError: String?
    private(set) var saving = false
    var draft = ClaudeCompactDraft()
    private(set) var notice: String?
    private(set) var saveError: String?

    func load(_ api: AgenthailAPI?) async {
        guard let api else { return }
        do {
            let response = try await api.claudeCompactSettings()
            roots = response.roots
            holds = response.idleCompactionHolds ?? []
            loadError = nil
            if !response.roots.contains(where: { $0.root == draft.root }) { draft.root = response.roots.first?.root ?? "" }
        } catch {
            loadError = AgenthailAPIError.userMessage(error)
        }
    }

    func save(_ api: AgenthailAPI?) async {
        guard let api, !saving, draft.problem == nil, draft.changes, !draft.root.isEmpty else { return }
        saving = true
        defer { saving = false }
        notice = nil
        saveError = nil
        do {
            _ = try await api.saveClaudeCompactSettings(draft.body)
            draft.saved()
            notice = "Saved."
            await load(api)
        } catch {
            saveError = AgenthailAPIError.userMessage(error)
        }
    }
}
