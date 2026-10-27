import SwiftUI

struct NewSessionSheet: View {
    @ObservedObject var model: AgenthailIOSModel
    @Environment(\.dismiss) private var dismiss
    @State private var hosts: CreationHostPicker
    @State private var selectedSurface = ""
    @State private var cwd = ""
    @State private var selectedModel = ""
    @State private var models: [ModelOption] = []
    @State private var message = ""
    @State private var error: String?
    @State private var modelError: String?
    @State private var loading = false
    @State private var claude = ClaudeCreationSettings()
    @State private var selectedLauncher: LauncherOption?
    @State private var showingModels = false
    @State private var turnSettings = TurnSettings()

    init(model: AgenthailIOSModel, place: NewSessionPlace? = nil) {
        self.model = model
        let selected = model.snapshot?.sessions.first { $0.id == model.selectedSessionID }
        var hosts = CreationHostPicker(hosts: model.creationHosts, selectedSession: selected)
        if let place { _ = hosts.select(place.host) }
        _hosts = State(initialValue: hosts)
        _cwd = State(initialValue: place?.folder ?? "")
    }

    private var options: SessionCreationOptions? { hosts.options }
    private var hostName: String { hosts.selectedHost?.name ?? "your computer" }

    var body: some View {
        NavigationStack {
            Form {
                if loading { ProgressView("Loading session options") }
                if let error = hosts.loadError ?? error { Section { Text(error).foregroundStyle(PhonePalette.failed); Button("Reload options") { Task { await load() } } } }
                if let options {
                    if options.surfaces.isEmpty {
                        ContentUnavailableView("No agent can start sessions", systemImage: "bubble.left", description: Text("Configure a supported runtime on \(hostName), then reload."))
                    } else {
                        Section("Agent") {
                            Picker("Run with", selection: $selectedSurface) {
                                ForEach(options.surfaces) { Text($0.id.capitalized).tag($0.id) }
                            }
                            Text(selectedSurface != "claude" ? "Uses the runtime configured on \(hostName)." : selectedLauncher?.id == "claude-bg" ? "Starts a Claude Code background job on \(hostName)." : "Starts a Claude session on \(hostName), run by Agenthail.").font(.footnote).foregroundStyle(PhonePalette.muted)
                        }
                        let launchers = launchers(for: selectedSurface, options: options)
                        if !launchers.isEmpty {
                            Section("Where it runs") {
                                Picker("Launcher", selection: $selectedLauncher) {
                                    Text("Use \(hostName) default").tag(LauncherOption?.none)
                                    ForEach(launchers) { launcher in
                                        Text(launcher.available ? launcher.label : "\(launcher.label) (unavailable)")
                                            .tag(Optional(launcher))
                                            .disabled(!launcher.available)
                                    }
                                }
                                ForEach(launchers.filter { !$0.available }) { launcher in
                                    if let detail = launcher.detail, !detail.isEmpty {
                                        Text("\(launcher.label): \(detail)").font(.footnote).foregroundStyle(PhonePalette.muted)
                                    }
                                }
                            }
                        }
                        if options.surfaces.first(where: { $0.id == selectedSurface })?.workspace == true {
                            Section("Project on \(hostName)") {
                                if !options.projects.isEmpty {
                                    Menu {
                                        ForEach(options.projects) { project in Button(project.displayName) { cwd = project.rootPath } }
                                    } label: {
                                        LabeledContent("Project", value: options.projects.first(where: { $0.rootPath == cwd })?.displayName ?? "Choose")
                                    }
                                    .accessibilityIdentifier("creation-project-picker")
                                }
                                TextField("~/projects/my-project", text: $cwd).textInputAutocapitalization(.never).autocorrectionDisabled()
                                Text("Pick a project or type a folder. Leave blank to use the home folder on \(hostName). The folder must already exist.").font(.footnote).foregroundStyle(PhonePalette.muted)
                            }
                        }
                        Section("Model") {
                            Button { showingModels = true } label: {
                                LabeledContent("Model", value: models.first(where: { $0.id == selectedModel })?.displayName ?? (selectedModel.isEmpty ? "Use default" : selectedModel))
                                    .contentShape(Rectangle())
                            }
                            .accessibilityIdentifier("creation-model-picker")
                            if let modelError { Text(modelError).font(.footnote).foregroundStyle(PhonePalette.muted) }
                            Text("Uses the runtime’s existing permission settings. Requests requiring approval may need attention on \(hostName).").font(.footnote).foregroundStyle(PhonePalette.muted)
                        }
                        if selectedSurface == "codex" {
                            TurnSettingsView(settings: $turnSettings, modelOption: selectedModelOption, enabled: selectedLauncher?.isTerminal != true)
                        }
                        if selectedSurface == "claude", hosts.claudeConfigRoots.count > 1 {
                            let roots = hosts.claudeConfigRoots
                            let titles = ClaudeRootLabels.titles(roots, host: hostName)
                            Section {
                                Picker("Account", selection: $claude.configRoot) {
                                    Text(ClaudeRootLabels.automatic(hosts.claudePick, roots: roots, titles: titles)).tag("")
                                    ForEach(Array(zip(roots, titles)), id: \.0.id) { root, title in
                                        Text(ClaudeRootLabels.option(root, title: title, pick: hosts.claudePick)).tag(root.root).selectionDisabled(!root.selectable)
                                    }
                                }
                            } footer: {
                                if claude.configRoot.isEmpty, let pick = hosts.claudePick {
                                    Text(ClaudeRootLabels.reason(pick, roots: roots, titles: titles))
                                }
                            }
                        }
                        Section("First instruction") {
                            TextField("What would you like the agent to do?", text: $message, axis: .vertical).lineLimit(4...12)
                        }
                        if selectedSurface == "claude" {
                            Section {
                                DisclosureGroup("Claude options") {
                                    TextField("Session name (optional)", text: $claude.name)
                                    TextField("New worktree name (optional)", text: $claude.worktree).textInputAutocapitalization(.never).autocorrectionDisabled()
                                    TextField("Named agent (optional)", text: $claude.agent).textInputAutocapitalization(.never).autocorrectionDisabled()
                                    Picker("Effort", selection: $claude.effort) {
                                        Text("Runtime default").tag("")
                                        ForEach(["low", "medium", "high", "xhigh", "max"], id: \.self) { Text($0.capitalized).tag($0) }
                                    }
                                    Picker("Permissions", selection: $claude.permissionMode) {
                                        Text("Runtime default").tag("")
                                        Text("Ask before changes").tag("manual")
                                        Text("Accept edits").tag("acceptEdits")
                                        Text("Automatic review").tag("auto")
                                        Text("Deny approval prompts").tag("dontAsk")
                                        Text("Bypass permissions").tag("bypassPermissions")
                                        Text("Plan only").tag("plan")
                                    }
                                    Text("Worktrees require a Git repository or configured worktree hooks. Named agents must exist in Claude’s configuration.").font(.footnote).foregroundStyle(PhonePalette.muted)
                                }
                            }.disabled(selectedLauncher?.isTerminal == true)
                        }
                    }
                }
                if let error = model.creationError { Section { Text(error).foregroundStyle(PhonePalette.failed) } }
                if let warning = model.creationWarning { Section { Text(warning).foregroundStyle(PhonePalette.attention) } }
            }
            .disabled(model.creatingSession)
            .navigationTitle("New session")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.disabled(model.creatingSession) }
                if hosts.isVisible {
                    ToolbarItem(placement: .principal) { hostButton }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button { Task {
                        if await model.createSession(surface: selectedSurface, message: message, cwd: cwd, model: selectedModel, turnSettings: turnSettings, claude: claude, launcher: selectedLauncher, host: hosts.hostQuery) { dismiss() }
                    } } label: {
                        if model.creatingSession { ProgressView() } else { Text("Start") }
                    }.disabled(model.creatingSession || model.creationWarning != nil || !hosts.canStart || selectedSurface.isEmpty || message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .interactiveDismissDisabled(model.creatingSession)
            .onAppear { model.prepareNewSessionForm() }
            .sheet(isPresented: $showingModels) {
                SearchableModelSelectionSheet(initialOptions: models, currentSelectedID: selectedModel.isEmpty ? nil : selectedModel, allowsDefault: true, onSelect: { value in
                    selectedModel = value ?? ""
                    turnSettings.effort = nil
                }, reload: {
                    let generation = hosts.generation
                    let values = try await model.creationModels(surface: selectedSurface, host: hosts.hostQuery)
                    guard generation == hosts.generation else { throw CancellationError() }
                    models = values
                    modelError = nil
                    return values
                })
            }
            .task(id: hosts.generation) { await load() }
            .onChange(of: model.creationHosts) { _, next in
                let before = hosts.generation
                hosts.update(hosts: next)
                if hosts.generation != before { cwd = ""; selectedSurface = ""; showingModels = false }
            }
            .task(id: "\(hosts.generation)/\(selectedSurface)") {
                selectedLauncher = nil
                models = []; selectedModel = ""; modelError = nil; turnSettings = .init(); claude = .init()
                guard !selectedSurface.isEmpty else { return }
                do { let values = try await model.creationModels(surface: selectedSurface, host: hosts.hostQuery); try Task.checkCancellation(); models = values }
                catch is CancellationError {} catch { modelError = "Models unavailable. You can still use the runtime default." }
            }
            .onChange(of: selectedLauncher) { _, launcher in
                guard launcher?.isTerminal == true else { return }
                turnSettings = .init()
                claude = ClaudeCreationSettings(configRoot: claude.configRoot)
            }
        }
    }
    private var selectedModelOption: ModelOption? {
        models.first { $0.id == selectedModel } ?? (selectedModel.isEmpty ? models.first { $0.default == true } : nil)
    }
    private func launchers(for surface: String, options: SessionCreationOptions) -> [LauncherOption] {
        options.launchers?.filter { $0.agents.contains(surface) } ?? []
    }
    private var hostButton: some View {
        Menu {
            ForEach(hosts.hosts) { host in
                Button {
                    if hosts.select(host.id) { cwd = ""; selectedSurface = "" }
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
            Label(hostName, systemImage: "desktopcomputer").labelStyle(.titleAndIcon).font(.subheadline)
        }
        .accessibilityIdentifier("creation-host-picker")
        .accessibilityLabel("Starts on \(hostName)")
    }
    private func load() async {
        loading = true; error = nil
        defer { loading = false }
        let generation = hosts.generation
        do {
            let loaded = try await model.sessionOptions(host: hosts.hostQuery)
            try Task.checkCancellation()
            hosts.receive(options: loaded, for: generation)
            if generation == hosts.generation {
                selectedSurface = loaded.surfaces.first?.id ?? ""
                if cwd.isEmpty { cwd = loaded.projects.first?.rootPath ?? "" }
            }
            loading = false
            let host = hosts.hostQuery
            if (loaded.claudeConfigRoots ?? []).count > 1, let pick = try? await model.creationRootPick(host: host) {
                hosts.receive(pick: pick, for: generation)
            }
            await withTaskGroup(of: ClaudeRootUsage?.self) { group in
                for root in loaded.claudeConfigRoots ?? [] where root.signedIn {
                    group.addTask { try? await model.creationRootUsage(root: root.root, host: host) }
                }
                for await usage in group {
                    if let usage { hosts.receive(usage: usage, for: generation) }
                }
            }
        } catch is CancellationError {
        } catch { hosts.fail(error.localizedDescription, for: generation) }
    }
}
