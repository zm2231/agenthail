import AppKit
import SwiftUI

struct NewSessionSheet: View {
    @ObservedObject var model: AgenthailModel
    @Environment(\.dismiss) private var dismiss
    @State private var form = SessionLaunchForm()
    @State private var models: [String: [ModelOption]] = [:]
    @State private var modelError: String?
    @State private var customModel = false
    @State private var showingOptions = false
    @State private var starting = false
    @State private var error: String?
    @State private var submitted: String?

    private enum ModelChoice: Hashable {
        case runtimeDefault
        case listed(String)
        case custom
    }

    private var launchers: [LauncherOption] {
        (model.creationOptions?.launchers ?? []).filter { $0.agents.contains(form.agent) }
    }

    private var agents: [String] {
        let supported = (model.creationOptions?.surfaces ?? []).map(\.id)
        if supported.isEmpty { return ["claude", "codex"] }
        return ["claude", "codex", "notion"].filter(supported.contains)
    }

    private var folders: [String] {
        var seen = Set<String>()
        return SessionTree.newestFirst(model.knownSessions.filter { $0.hostProject != nil })
            .compactMap { $0.checkout?.path ?? $0.cwd }
            .filter { !$0.hasPrefix("/private/") && !$0.hasPrefix("/var/") && !$0.contains("/.no-mistakes/") && seen.insert($0).inserted }
            .prefix(12)
            .map { $0 }
    }

    private var agentModels: [ModelOption] { models[form.agent] ?? [] }
    private var allowsCustomModel: Bool { agentModels.contains { $0.allowsCustom == true } }

    private var canStart: Bool {
        !starting && submitted == nil && form.isComplete
            && (form.launcher == nil || launchers.contains { $0.id == form.launcher && $0.available })
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New session")
                .font(.system(size: 15, weight: .semibold))
            Form {
                Section {
                    Picker("Agent", selection: Binding(get: { form.agent }, set: { form.switchAgent(to: $0) })) {
                        ForEach(agents, id: \.self) { Text($0.capitalized).tag($0) }
                    }
                    if !launchers.isEmpty {
                        Picker("Runs in", selection: Binding(get: { form.launcher }, set: { form.selectLauncher($0) })) {
                            Text("Default").tag(nil as String?)
                                .help("Starts the agent in the background on this Mac.")
                            ForEach(launchers) { option in
                                Text(option.available ? option.label : "\(option.label) (unavailable)")
                                    .tag(Optional(option.id))
                                    .selectionDisabled(!option.available)
                                    .help(option.detail ?? "")
                            }
                        }
                    }
                    TextField("Name", text: $form.alias, prompt: Text("Optional"))
                        .help("Other agents and links can reach the session as @name.")
                    if form.usesFolder {
                        HStack {
                            Picker("Folder", selection: $form.folder) {
                                if form.folder.isEmpty { Text("Choose a folder").tag("") }
                                ForEach(folders, id: \.self) { path in
                                    Text((path as NSString).abbreviatingWithTildeInPath).tag(path)
                                }
                                if !form.folder.isEmpty && !folders.contains(form.folder) {
                                    Text((form.folder as NSString).abbreviatingWithTildeInPath).tag(form.folder)
                                }
                            }
                            Button("Choose…", action: chooseFolder)
                        }
                    }
                    if form.listsModels {
                        modelPicker
                    }
                }
                Section {
                    TextField("First message", text: $form.message, axis: .vertical)
                        .lineLimit(3...8)
                }
                if form.agent == "codex" || form.agent == "claude" {
                    Section {
                        DisclosureGroup(isExpanded: $showingOptions) {
                            if form.hasOptions {
                                if form.agent == "codex" { codexOptions } else { claudeOptions }
                            } else {
                                Text("Sessions started in a terminal use the agent's own settings.")
                                    .font(.system(size: 12))
                                    .foregroundStyle(DesktopPalette.text2)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                            }
                        } label: {
                            Button(form.agent == "codex" ? "Codex options" : "Claude options") { showingOptions.toggle() }
                                .buttonStyle(.plain)
                        }
                    }
                }
            }
            .formStyle(.grouped)
            if let message = error ?? form.problem {
                Text(message)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.red)
            }
            if let submitted {
                Text(submitted)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            HStack {
                Spacer()
                if submitted != nil {
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
        .padding(20)
        .frame(width: 520)
        .disabled(starting)
        .task {
            takeSharedMessage()
            await model.loadCreationOptions()
            if !agents.contains(form.agent), let first = agents.first { form.switchAgent(to: first) }
            if form.folder.isEmpty { form.folder = model.mainPane.selectedSession.flatMap { $0.checkout?.path ?? $0.cwd } ?? folders.first ?? "" }
        }
        .task(id: form.agent) { await loadModels() }
        .onChange(of: form.agent) {
            customModel = false
            error = nil
        }
        .onChange(of: model.newSessionMessage) { takeSharedMessage() }
        .onChange(of: starting) { takeSharedMessage() }
    }

    @ViewBuilder private var modelPicker: some View {
        Picker("Model", selection: Binding(get: { modelChoice }, set: selectModel)) {
            Text("Runtime default").tag(ModelChoice.runtimeDefault)
            ForEach(agentModels) { option in
                Text(option.default == true ? "\(option.displayName) (default)" : option.displayName)
                    .tag(ModelChoice.listed(option.id))
                    .help(option.description ?? option.id)
            }
            if allowsCustomModel || modelError != nil || (!form.model.isEmpty && !agentModels.contains { $0.id == form.model }) {
                Text("Custom model ID").tag(ModelChoice.custom)
            }
        }
        if modelChoice == .custom {
            TextField("Model ID", text: Binding(get: { form.model }, set: { form.selectModel($0) }), prompt: Text("Exact model ID"))
        }
        if let modelError {
            Text(modelError)
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
        }
    }

    private var modelChoice: ModelChoice {
        if customModel { return .custom }
        if form.model.isEmpty { return .runtimeDefault }
        return agentModels.contains { $0.id == form.model } ? .listed(form.model) : .custom
    }

    private func selectModel(_ choice: ModelChoice) {
        customModel = choice == .custom
        switch choice {
        case .runtimeDefault: form.selectModel("")
        case .listed(let id): form.selectModel(id)
        case .custom: if agentModels.contains(where: { $0.id == form.model }) { form.selectModel("") }
        }
    }

    @ViewBuilder private var effortPicker: some View {
        Picker("Effort", selection: $form.effort) {
            Text("Runtime default").tag("")
            ForEach(form.efforts(models: agentModels), id: \.self) { Text($0.capitalized).tag($0) }
        }
    }

    @ViewBuilder private var codexOptions: some View {
        Picker("Approvals", selection: $form.codex.approvalPolicy) {
            Text("Runtime default").tag("")
            Text("Ask when needed").tag("on-request")
            Text("Ask for untrusted work").tag("untrusted")
            Text("Never ask").tag("never")
        }
        Picker("Mode", selection: $form.mode) {
            Text("Session default").tag(nil as TurnSettings.Mode?)
            Text("Work").tag(TurnSettings.Mode.default as TurnSettings.Mode?)
            Text("Plan").tag(TurnSettings.Mode.plan as TurnSettings.Mode?)
        }
        effortPicker
        Picker("Service tier", selection: $form.codex.serviceTier) {
            Text("Runtime default").tag("")
            Text("Default").tag("default")
            Text("Fast").tag("fast")
            Text("Flex").tag("flex")
        }
        TextField(text: $form.codex.outputSchema, prompt: Text("Optional JSON schema for the final answer"), axis: .vertical) {
            Text("Output schema").font(.body)
        }
        .lineLimit(2...8)
        .font(.system(size: 12, design: .monospaced))
    }

    @ViewBuilder private var claudeOptions: some View {
        TextField("Named agent", text: $form.claudeAgent, prompt: Text("Optional"))
            .help("The agent must exist in Claude's configuration.")
        TextField("New worktree", text: $form.worktree, prompt: Text("Optional worktree name"))
            .help("Needs a Git repository or configured worktree hooks.")
        Picker("Permissions", selection: $form.permissionMode) {
            Text("Runtime default").tag("")
            Text("Ask before changes").tag("manual")
            Text("Accept edits").tag("acceptEdits")
            Text("Automatic review").tag("auto")
            Text("Deny approval prompts").tag("dontAsk")
            Text("Plan only").tag("plan")
        }
        effortPicker
    }

    private func loadModels() async {
        modelError = nil
        let agent = form.agent
        guard form.listsModels, models[agent] == nil else { return }
        do {
            let options = try await model.creationModels(for: agent)
            try Task.checkCancellation()
            models[agent] = options
        } catch is CancellationError {
        } catch {
            if !error.isCancellation { modelError = "Models unavailable. The runtime default is used unless you enter a model ID." }
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
            case .submitted(let note), .starting(let note): submitted = note
            case .halted(let failure):
                error = failure
                submitted = "Check the sidebar before starting it again."
            case .uncertain(let failure), .failed(let failure): error = failure
            }
        }
    }
}
