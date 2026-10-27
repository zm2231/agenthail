import AppKit
import SwiftUI

struct NewSessionView: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var draft: NewSessionDraft
    @ObservedObject private var shortcuts = ShortcutStore.shared
    @State private var focused = false

    var body: some View {
        VStack(spacing: DesktopSpace.s) {
            VStack(alignment: .leading, spacing: 0) {
                ComposerTextView(text: $draft.form.message, focused: $focused, placeholder: "What should the agent do?", accessibilityLabel: "First message", lines: 4...14) { draft.start() }
                    .padding(.horizontal, 16)
                    .padding(.top, 15)
                    .padding(.bottom, DesktopSpace.s)
                HStack(alignment: .bottom, spacing: DesktopSpace.xs) {
                    ChipFlow(spacing: DesktopSpace.xs) { options }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button(action: draft.start) {
                        SendButtonFace(symbol: draft.starting ? "ellipsis" : "arrow.up", enabled: draft.canStart)
                    }
                    .buttonStyle(.plain)
                    .disabled(!draft.canStart)
                    .keyboardShortcut(shortcuts.keyboardShortcut(.send))
                    .accessibilityLabel(draft.starting ? "Starting" : "Start session")
                    .help(shortcuts.help("Start the session", .send))
                }
                .padding(.horizontal, DesktopSpace.s)
                .padding(.bottom, DesktopSpace.s)
            }
            .floatingGlass(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: DesktopRadius.sheet))
            .scrimSurface(radius: DesktopRadius.sheet)
            .floatingEdge(RoundedRectangle(cornerRadius: DesktopRadius.sheet))
            .disabled(draft.starting || draft.submitted != nil)
            status
        }
        .frame(maxWidth: 680)
        .padding(.horizontal, DesktopSpace.xxl)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .backdropSurface(DesktopPalette.window)
        .onAppear { focused = true }
        .task(id: "\(draft.hosts.generation)/\(model.isConnected)") {
            if model.isConnected { await draft.loadOptions() }
        }
        .task(id: "\(draft.hosts.generation)/\(draft.form.agent)/\(draft.hosts.options != nil)/\(model.isConnected)") { await draft.loadModels() }
        .onChange(of: model.snapshot?.daemon) { draft.daemonChanged() }
        .onChange(of: draft.form.agent) { draft.error = nil }
    }

    @ViewBuilder private var options: some View {
        chip(agentLabel, symbol: "sparkle", help: "Agent, account and model") {
            Picker("Agent", selection: Binding(get: { draft.form.agent }, set: { draft.form.switchAgent(to: $0) })) {
                ForEach(draft.agents, id: \.self) { Text($0.capitalized).tag($0) }
            }
            .pickerStyle(.inline)
            if draft.claudeConfigRoots.count > 1 {
                Divider()
                Picker("Account", selection: $draft.form.configRoot) {
                    Text(draft.hosts.claudePick?.menuLabel ?? "Automatic").tag("")
                    ForEach(draft.claudeConfigRoots) { root in
                        Text(root.menuLabel).tag(root.root).selectionDisabled(!root.signedIn)
                    }
                }
                .pickerStyle(.inline)
            }
            if draft.form.listsModels {
                Divider()
                Picker("Model", selection: Binding(get: { draft.form.model }, set: { draft.form.selectModel($0) })) {
                    if draft.listedDefault == nil { Text("Runtime default").tag("") }
                    ForEach(draft.agentModels) { option in
                        Text(option.displayName).tag(option.default == true ? "" : option.id)
                    }
                }
                .pickerStyle(.inline)
                if !draft.efforts.isEmpty {
                    Picker("Effort", selection: $draft.form.effort) {
                        Text("Runtime default").tag("")
                        ForEach(draft.efforts, id: \.self) { Text($0.capitalized).tag($0) }
                    }
                }
            }
            if !draft.launchers.isEmpty && draft.form.launcher == nil {
                Divider()
                launcherPicker
            }
        }
        placeChip
        if !draft.launchers.isEmpty, let launcher = draft.form.launcher {
            chip(launcher.label, symbol: "terminal", help: "Where the agent runs") { launcherPicker }
        }
    }

    private var launcherPicker: some View {
        Picker("Runs in", selection: Binding(get: { draft.form.launcher?.id }, set: { id in draft.form.selectLauncher(draft.launchers.first { $0.id == id }) })) {
            Text("Default").tag(nil as String?)
            ForEach(draft.launchers) { option in
                Text(option.available ? option.label : "\(option.label) (unavailable)")
                    .tag(Optional(option.id))
                    .selectionDisabled(!option.available)
            }
        }
    }

    @ViewBuilder private var placeChip: some View {
        if draft.form.usesFolder || draft.hosts.isVisible {
            chip(placeLabel, symbol: draft.startsOnThisMac ? "folder" : "desktopcomputer", help: draft.form.folder.isEmpty ? "Choose the computer and project" : "\(draft.hostName): \(draft.form.folder)") {
                if draft.hosts.isVisible {
                    Section("Computer") {
                        ForEach(draft.hosts.hosts) { host in
                            Button {
                                draft.selectHost(host.id)
                            } label: {
                                if host.id == draft.hosts.selectedHost?.id {
                                    Label(host.name, systemImage: "checkmark")
                                } else {
                                    Text(host.isAvailable ? host.name : "\(host.name) (\(host.stateLabel))")
                                }
                            }
                            .disabled(!host.isAvailable)
                        }
                    }
                }
                if draft.form.usesFolder {
                    Section("Project") {
                        ForEach(draft.hosts.projects) { project in
                            Button(project.displayName) { draft.form.folder = project.rootPath }
                        }
                        if draft.startsOnThisMac {
                            Button("Choose…", action: chooseFolder)
                        }
                    }
                }
            }
        }
        if draft.form.usesFolder && !draft.startsOnThisMac {
            TextField("Folder on \(draft.hostName)", text: $draft.form.folder)
                .textFieldStyle(.plain)
                .font(.system(size: DesktopType.secondary))
                .frame(width: 180)
                .padding(.horizontal, 8)
                .frame(height: 26)
                .overlay(Capsule().strokeBorder(DesktopPalette.line))
        }
    }

    private func chip<Content: View>(_ title: String, symbol: String, help: String, @ViewBuilder content: () -> Content) -> some View {
        Menu(content: content) {
            Label(title, systemImage: symbol)
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .font(.system(size: DesktopType.secondary))
        .foregroundStyle(DesktopPalette.text2)
        .padding(.horizontal, 8)
        .frame(height: 26)
        .overlay(Capsule().strokeBorder(DesktopPalette.line))
        .help(help)
    }

    @ViewBuilder private var status: some View {
        let lines = model.isConnected ? [draft.hosts.loadError.map { NewSessionNotice(summary: $0) }, draft.modelError].compactMap { $0 } : []
        if !model.isConnected || !lines.isEmpty || draft.error != nil || draft.submitted != nil {
            VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                if !model.isConnected {
                    HStack(spacing: DesktopSpace.s) {
                        Text(model.daemonSlow ? "Agenthail is running but took too long to answer. Retrying." : model.connectionError ?? "The app can't reach the Agenthail daemon.")
                            .foregroundStyle(DesktopPalette.text2)
                        Spacer(minLength: 0)
                        if !model.daemonSlow { Button("Start Agenthail") { model.restartDaemon() } }
                    }
                }
                ForEach(lines, id: \.self) { notice in
                    if let detail = notice.detail {
                        DisclosureGroup {
                            Text(detail)
                                .foregroundStyle(DesktopPalette.text2)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        } label: {
                            Text(notice.summary).foregroundStyle(DesktopPalette.text2)
                        }
                    } else {
                        Text(notice.summary).foregroundStyle(DesktopPalette.text2)
                    }
                }
                if let error = draft.error { Text(error).foregroundStyle(DesktopPalette.failed) }
                if let submitted = draft.submitted {
                    HStack(spacing: DesktopSpace.s) {
                        Text(submitted).foregroundStyle(DesktopPalette.text2)
                        Spacer(minLength: 0)
                        if draft.canStartAgain {
                            Button("Start again", action: draft.startAgain)
                                .help("Starts a new session even if the first one started.")
                        }
                        Button("New message", action: draft.reset)
                    }
                }
            }
            .font(.system(size: DesktopType.secondary))
            .padding(DesktopSpace.s)
            .frame(maxWidth: .infinity, alignment: .leading)
            .floatingGlass(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
            .scrimSurface(radius: DesktopRadius.card)
            .floatingEdge(RoundedRectangle(cornerRadius: DesktopRadius.card))
        }
    }

    private var folderLabel: String {
        if draft.form.folder.isEmpty { return "Choose a project" }
        if let project = draft.hosts.projects.first(where: { $0.rootPath == draft.form.folder }) { return project.displayName }
        return (draft.form.folder as NSString).lastPathComponent
    }

    private var agentLabel: String {
        let chosen = draft.form.model.isEmpty ? nil : draft.agentModels.first { $0.id == draft.form.model }?.displayName ?? draft.form.model
        let account = NewSessionLabels.account(draft.claudeConfigRoots, choice: draft.form.configRoot, pick: draft.hosts.claudePick)
        return NewSessionLabels.agent(draft.form.agent, model: draft.form.listsModels ? chosen : nil, effort: draft.form.effort, account: account)
    }

    private var placeLabel: String {
        draft.form.usesFolder ? NewSessionLabels.place(folderLabel, host: draft.startsOnThisMac ? nil : draft.hostName) : draft.hostName
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        if !draft.form.folder.isEmpty { panel.directoryURL = URL(fileURLWithPath: draft.form.folder) }
        if panel.runModal() == .OK, let url = panel.url { draft.form.folder = url.path }
    }
}
