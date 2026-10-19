import AppKit
import SwiftUI

struct NewSessionSheet: View {
    @ObservedObject var model: AgenthailModel
    @Environment(\.dismiss) private var dismiss
    @State private var form = SessionLaunchForm()
    @State private var models: [String: [ModelOption]] = [:]
    @State private var modelError: String?
    @State private var starting = false
    @State private var error: String?
    @State private var submitted: String?
    @State private var canStartAgain = false

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
        return SessionTree.newestFirst(model.knownSessions.filter { $0.project != nil })
            .compactMap { $0.checkout?.path ?? $0.cwd }
            .filter { !$0.hasPrefix("/private/") && !$0.hasPrefix("/var/") && !$0.contains("/.no-mistakes/") && seen.insert($0).inserted }
            .prefix(12)
            .map { $0 }
    }

    private var agentModels: [ModelOption] { models[form.agent] ?? [] }
    private var listedDefault: ModelOption? { agentModels.first { $0.default == true } }
    private var efforts: [String] { form.setsEffort ? form.efforts(models: agentModels) : [] }

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
            }
            .formStyle(.grouped)
            if let message = error {
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
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
        }
    }

    private var modelSummary: String {
        let selected = form.model.isEmpty ? listedDefault : agentModels.first { $0.id == form.model }
        let name = selected?.displayName ?? "Runtime default"
        return form.effort.isEmpty ? name : "\(name) · \(form.effort.capitalized)"
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
