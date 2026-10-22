import AppKit
import SwiftUI

struct NewSessionSheet: View {
    @ObservedObject var model: AgenthailModel
    @Environment(\.dismiss) private var dismiss
    @State private var form: SessionLaunchForm
    @State private var hosts: CreationHostPicker
    @State private var modelError: String?
    @State private var starting = false
    @State private var error: String?
    @State private var submitted: String?
    @State private var canStartAgain = false

    init(model: AgenthailModel) {
        self.model = model
        let hosts = CreationHostPicker(daemon: model.snapshot?.daemon, selectedSession: model.mainPane.selectedSession)
        _hosts = State(initialValue: hosts)
        _form = State(initialValue: SessionLaunchForm(host: hosts.hostQuery))
    }

    private var launchers: [LauncherOption] {
        (hosts.options?.launchers ?? []).filter { $0.agents.contains(form.agent) }
    }

    private var agents: [String] {
        let supported = (hosts.options?.surfaces ?? []).map(\.id)
        if supported.isEmpty { return ["claude", "codex"] }
        return ["claude", "codex", "notion"].filter(supported.contains)
    }

    private var projects: [LaunchProject] { hosts.projects }
    private var claudeConfigRoots: [ClaudeConfigRootOption] { form.agent == "claude" ? hosts.claudeConfigRoots : [] }
    private var startsOnThisMac: Bool { hosts.hostQuery == nil }

    private var agentModels: [ModelOption] { hosts.models[form.agent] ?? [] }
    private var listedDefault: ModelOption? { agentModels.first { $0.default == true } }
    private var efforts: [String] { form.setsEffort ? form.efforts(models: agentModels) : [] }

    private var canStart: Bool {
        !starting && submitted == nil && form.isComplete && hosts.canStart
            && (form.launcher == nil || launchers.contains { $0.id == form.launcher?.id && $0.available })
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.l) {
            HStack {
                Text("New session")
                    .font(.system(size: DesktopType.title, weight: .semibold))
                Spacer()
                if hosts.isVisible { hostButton }
            }
            if let failure = hosts.loadError {
                Text(failure)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.failed)
            }
            Form {
                Section {
                    Picker("Agent", selection: Binding(get: { form.agent }, set: { form.switchAgent(to: $0) })) {
                        ForEach(agents, id: \.self) { Text($0.capitalized).tag($0) }
                    }
                    if !launchers.isEmpty {
                        Picker("Runs in", selection: Binding(get: { form.launcher?.id }, set: { id in form.selectLauncher(launchers.first { $0.id == id }) })) {
                            Text("Default").tag(nil as String?)
                                .help("Starts the agent on \(startsOnThisMac ? "this Mac" : hosts.selectedHost?.name ?? "the host") without a terminal.")
                            ForEach(launchers) { option in
                                Text(option.available ? option.label : "\(option.label) (unavailable)")
                                    .tag(Optional(option.id))
                                    .selectionDisabled(!option.available)
                                    .help(option.detail ?? "")
                            }
                        }
                    }
                    if form.usesFolder && !startsOnThisMac {
                        HStack {
                            TextField("Folder on \(hosts.selectedHost?.name ?? "host")", text: $form.folder)
                            if !projects.isEmpty {
                                Menu("Projects") {
                                    ForEach(projects) { project in Button(project.displayName) { form.folder = project.rootPath }.help(project.rootPath) }
                                }
                                .fixedSize()
                            }
                        }
                    } else if form.usesFolder {
                        HStack {
                            Picker("Project", selection: $form.folder) {
                                if form.folder.isEmpty { Text("Choose a project").tag("") }
                                ForEach(projects) { project in
                                    Text(project.displayName).tag(project.rootPath).help(project.rootPath)
                                }
                                if !form.folder.isEmpty && !projects.contains(where: { $0.rootPath == form.folder }) {
                                    Text((form.folder as NSString).abbreviatingWithTildeInPath).tag(form.folder)
                                }
                            }
                            Button("Choose…", action: chooseFolder)
                        }
                    }
                    if claudeConfigRoots.count > 1 {
                        Picker("Config folder", selection: $form.configRoot) {
                            ForEach(claudeConfigRoots) { root in
                                Text(root.menuLabel)
                                    .tag(root.choice)
                                    .selectionDisabled(!root.signedIn)
                                    .help(root.root)
                            }
                        }
                    }
                    if form.listsModels {
                        modelPicker
                    }
                }
                Section {
                    FirstMessageField(message: $form.message)
                }
            }
            .formStyle(.grouped)
            if let message = error {
                Text(message)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.failed)
            }
            if let submitted {
                Text(submitted)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
            }
            HStack {
                Spacer()
                if submitted != nil {
                    if canStartAgain {
                        Button("Start again", action: startAgain)
                            .help("Starts a new session even if the first one started.")
                    }
                    Button("Done") { dismiss() }
                        .keyboardShortcut(.defaultAction)
                        .buttonStyle(.borderedProminent)
                } else {
                    Button("Cancel") { dismiss() }
                        .keyboardShortcut(.cancelAction)
                    Button(starting ? "Starting…" : "Start") { start() }
                        .keyboardShortcut(.return, modifiers: .command)
                        .buttonStyle(.borderedProminent)
                        .disabled(!canStart)
                }
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 520)
        .disabled(starting)
        .task {
            takeSharedMessage()
        }
        .task(id: hosts.generation) { await loadOptions() }
        .task(id: "\(hosts.generation)/\(form.agent)/\(hosts.options != nil)") { await loadModels() }
        .onChange(of: model.snapshot?.daemon) {
            hosts.update(daemon: model.snapshot?.daemon)
            if form.host != hosts.hostQuery { form.switchHost(to: hosts.hostQuery, folder: "") }
        }
        .onChange(of: form.agent) { error = nil }
        .onChange(of: model.newSessionMessage) { takeSharedMessage() }
        .onChange(of: starting) { takeSharedMessage() }
    }

    @ViewBuilder private var modelPicker: some View {
        LabeledContent("Model") {
            Menu {
                Picker("Model", selection: Binding(get: { form.model }, set: { form.selectModel($0) })) {
                    if listedDefault == nil { Text("Runtime default").tag("") }
                    ForEach(agentModels) { option in
                        Text(option.displayName)
                            .tag(option.default == true ? "" : option.id)
                            .help(option.description ?? option.id)
                    }
                }
                .pickerStyle(.inline)
                if !efforts.isEmpty {
                    Picker("Effort", selection: $form.effort) {
                        Text("Runtime default").tag("")
                        ForEach(efforts, id: \.self) { Text($0.capitalized).tag($0) }
                    }
                }
            } label: {
                Text(modelSummary)
            }
            .fixedSize()
        }
        if let modelError {
            Text(modelError)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
        }
    }

    private var modelSummary: String {
        let selected = form.model.isEmpty ? listedDefault : agentModels.first { $0.id == form.model }
        let name = selected?.displayName ?? "Runtime default"
        return form.effort.isEmpty ? name : "\(name) · \(form.effort.capitalized)"
    }

    @ViewBuilder private var hostButton: some View {
        Menu {
            ForEach(hosts.hosts) { host in
                Button {
                    selectHost(host.id)
                } label: {
                    if host.id == hosts.selectedHost?.id {
                        Label(host.name, systemImage: "checkmark")
                    } else {
                        Text(host.isAvailable ? host.name : "\(host.name) (\(host.stateLabel))")
                    }
                }
                .disabled(!host.isAvailable)
            }
        } label: {
            Label(hosts.selectedHost?.name ?? "This Mac", systemImage: "desktopcomputer")
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .help("Choose the machine this session starts on")
        .accessibilityLabel("Starts on \(hosts.selectedHost?.name ?? "this Mac")")
    }

    private func selectHost(_ id: String) {
        guard hosts.select(id) else { return }
        let selected = model.mainPane.selectedSession
        form.switchHost(to: hosts.hostQuery, folder: hosts.owns(selected) ? selected.flatMap { $0.checkout?.path ?? $0.cwd } ?? "" : "")
        modelError = nil
        error = nil
    }

    private func loadOptions() async {
        let generation = hosts.generation
        do {
            let options = try await model.creationOptions(host: hosts.hostQuery)
            try Task.checkCancellation()
            hosts.receive(options: options, for: generation)
            if !agents.contains(form.agent), let first = agents.first { form.switchAgent(to: first) }
            if form.folder.isEmpty {
                form.folder = hosts.defaultFolder(selected: model.mainPane.selectedSession)
            }
            await loadRootUsage(options, for: generation)
        } catch is CancellationError {
        } catch {
            if !error.isCancellation { hosts.fail("Couldn't load what \(hosts.selectedHost?.name ?? "this Mac") can start: \(AgenthailAPIError.userMessage(error))", for: generation) }
        }
    }

    /// Fills in each signed-in root's usage as it arrives; the roots are
    /// already shown.
    private func loadRootUsage(_ options: SessionCreationOptions, for generation: Int) async {
        let host = hosts.hostQuery
        await withTaskGroup(of: ClaudeRootUsage?.self) { group in
            for root in options.claudeConfigRoots ?? [] where root.signedIn {
                group.addTask { try? await model.creationRootUsage(root: root.root, host: host) }
            }
            for await usage in group {
                if let usage { hosts.receive(usage: usage, for: generation) }
            }
        }
    }

    private func loadModels() async {
        modelError = nil
        let agent = form.agent
        let generation = hosts.generation
        guard form.listsModels, hosts.options != nil, hosts.models[agent] == nil else { return }
        do {
            let options = try await model.creationModels(for: agent, host: hosts.hostQuery)
            try Task.checkCancellation()
            hosts.receive(models: options, agent: agent, for: generation)
        } catch is CancellationError {
        } catch {
            if !error.isCancellation { modelError = "Models unavailable: \(error.localizedDescription)\nThe session uses the runtime default." }
        }
    }

    private func takeSharedMessage() {
        guard !starting, submitted == nil, let shared = model.newSessionMessage else { return }
        model.newSessionMessage = nil
        form.message = form.trimmedMessage.isEmpty ? shared : "\(form.message)\n\n\(shared)"
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        if !form.folder.isEmpty { panel.directoryURL = URL(fileURLWithPath: form.folder) }
        if panel.runModal() == .OK, let url = panel.url { form.folder = url.path }
    }

    private func start() {
        starting = true
        error = nil
        let request = form
        Task {
            let outcome = await model.launchSession(request)
            starting = false
            switch outcome {
            case .opened: dismiss()
            case .submitted(let note), .starting(let note), .unconfirmed(let note): submitted = note
            case .halted(let failure):
                error = failure
                submitted = "Check the sidebar before starting it again."
            case .uncertain(let failure), .failed(let failure): error = failure
            }
            canStartAgain = outcome.offersStartAgain
        }
    }

    private func startAgain() {
        model.startNewLaunch()
        submitted = nil
        canStartAgain = false
        start()
    }
}
