import Combine
import Foundation

/// What the New Session view is composing. The pane keeps it while the view
/// is open, so switching tabs never loses a half-written first message.
@MainActor
final class NewSessionDraft: ObservableObject {
    unowned let model: AgenthailModel
    private let origin: SessionState?
    @Published var form: SessionLaunchForm
    @Published var hosts: CreationHostPicker
    @Published var modelError: NewSessionNotice?
    @Published private(set) var starting = false
    @Published var error: String?
    @Published private(set) var submitted: String?
    @Published private(set) var canStartAgain = false
    private var held: [String] = []

    init(model: AgenthailModel, selected: SessionState?) {
        self.model = model
        origin = selected
        let hosts = CreationHostPicker(daemon: model.snapshot?.daemon, selectedSession: selected)
        self.hosts = hosts
        form = SessionLaunchForm(host: hosts.hostQuery)
    }

    var launchers: [LauncherOption] {
        (hosts.options?.launchers ?? []).filter { $0.agents.contains(form.agent) }
    }

    var agents: [String] {
        let supported = (hosts.options?.surfaces ?? []).map(\.id)
        if supported.isEmpty { return ["claude", "codex"] }
        return ["claude", "codex", "notion"].filter(supported.contains)
    }

    var claudeConfigRoots: [ClaudeConfigRootOption] { form.agent == "claude" ? hosts.claudeConfigRoots : [] }
    var startsOnThisMac: Bool { hosts.hostQuery == nil }
    var hostName: String { hosts.selectedHost?.name ?? "this Mac" }
    var agentModels: [ModelOption] { hosts.models[form.agent] ?? [] }
    var listedDefault: ModelOption? { agentModels.first { $0.default == true } }
    var efforts: [String] { form.setsEffort ? form.efforts(models: agentModels) : [] }

    var editable: Bool { !starting && submitted == nil }

    var canStart: Bool {
        model.isConnected && !starting && submitted == nil && form.isComplete && hosts.canStart
            && (form.launcher == nil || launchers.contains { $0.id == form.launcher?.id && $0.available })
    }

    /// Adds shared text to the first message, or holds it until the draft
    /// can be edited again.
    func take(_ message: String) {
        guard editable else {
            held.append(message)
            return
        }
        form.message = form.trimmedMessage.isEmpty ? message : "\(form.message)\n\n\(message)"
    }

    /// Hands over text that arrived while a launch was in flight.
    func takeHeld() -> [String] {
        defer { held = [] }
        return held
    }

    private func releaseHeld() {
        guard editable else { return }
        takeHeld().forEach(take)
    }

    func daemonChanged() {
        hosts.update(daemon: model.snapshot?.daemon)
        if form.host != hosts.hostQuery { form.switchHost(to: hosts.hostQuery, folder: "") }
    }

    func selectHost(_ id: String) {
        guard hosts.select(id) else { return }
        form.switchHost(to: hosts.hostQuery, folder: hosts.owns(origin) ? origin.flatMap { $0.checkout?.path ?? $0.cwd } ?? "" : "")
        modelError = nil
        error = nil
    }

    func place(host: String?, folder: String) {
        _ = hosts.select(host ?? "")
        form.switchHost(to: hosts.hostQuery, folder: folder)
        form.folder = folder
        modelError = nil
        error = nil
    }

    func loadOptions() async {
        let generation = hosts.generation
        do {
            let options = try await model.creationOptions(host: hosts.hostQuery)
            try Task.checkCancellation()
            hosts.receive(options: options, for: generation)
            if !agents.contains(form.agent), let first = agents.first { form.switchAgent(to: first) }
            if form.folder.isEmpty { form.folder = hosts.defaultFolder(selected: origin) }
            async let pick: Void = loadRootPick(options, for: generation)
            await loadRootUsage(options, for: generation)
            await pick
        } catch is CancellationError {
        } catch {
            if !error.isCancellation { hosts.fail("Couldn't load what \(hostName) can start: \(AgenthailAPIError.userMessage(error))", for: generation) }
        }
    }

    private func loadRootPick(_ options: SessionCreationOptions, for generation: Int) async {
        guard (options.claudeConfigRoots ?? []).count > 1, let pick = try? await model.creationRootPick(host: hosts.hostQuery) else { return }
        hosts.receive(pick: pick, for: generation)
    }

    /// Fills in each signed-in root's usage as it arrives; the roots are
    /// already shown.
    private func loadRootUsage(_ options: SessionCreationOptions, for generation: Int) async {
        let host = hosts.hostQuery
        let model = model
        await withTaskGroup(of: ClaudeRootUsage?.self) { group in
            for root in options.claudeConfigRoots ?? [] where root.readsUsage {
                group.addTask { try? await model.creationRootUsage(root: root.root, host: host) }
            }
            for await usage in group {
                if let usage { hosts.receive(usage: usage, for: generation) }
            }
        }
    }

    func loadModels() async {
        modelError = nil
        let agent = form.agent
        let generation = hosts.generation
        guard model.isConnected, form.listsModels, hosts.options != nil, hosts.models[agent] == nil else { return }
        do {
            let options = try await model.creationModels(for: agent, host: hosts.hostQuery)
            try Task.checkCancellation()
            hosts.receive(models: options, agent: agent, for: generation)
        } catch is CancellationError {
        } catch {
            if !error.isCancellation { modelError = NewSessionNotice(summary: "The model list isn't available, so the session uses the default model.", detail: error.localizedDescription) }
        }
    }

    func start() {
        guard canStart else { return }
        starting = true
        error = nil
        let request = form
        Task {
            let outcome = await model.launchSession(request)
            starting = false
            switch outcome {
            case .opened: break
            case .submitted(let note), .starting(let note), .unconfirmed(let note): submitted = note
            case .halted(let failure):
                error = failure
                submitted = "Check the sidebar before starting it again."
            case .uncertain(let failure), .failed(let failure): error = failure
            }
            canStartAgain = outcome.offersStartAgain
            releaseHeld()
        }
    }

    func startAgain() {
        model.startNewLaunch()
        submitted = nil
        canStartAgain = false
        start()
    }

    func reset() {
        form.message = ""
        submitted = nil
        canStartAgain = false
        error = nil
        releaseHeld()
    }
}

/// A short sentence for New Session, with any technical detail kept behind
/// a disclosure.
struct NewSessionNotice: Hashable {
    let summary: String
    var detail: String? = nil
}
