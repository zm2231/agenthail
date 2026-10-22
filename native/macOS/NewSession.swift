import AppKit
import SwiftUI

struct NewSessionView: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var draft: NewSessionDraft
    @ObservedObject private var shortcuts = ShortcutStore.shared
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: DesktopSpace.s) {
            VStack(alignment: .leading, spacing: 0) {
                TextField("What should the agent do?", text: $draft.form.message, axis: .vertical)
                    .textFieldStyle(.plain)
                    .font(.system(size: DesktopType.prose))
                    .lineLimit(4...14)
                    .focused($focused)
                    .onSubmit { draft.start() }
                    .padding(.horizontal, 16)
                    .padding(.top, 15)
                    .padding(.bottom, DesktopSpace.s)
                    .accessibilityLabel("First message")
                HStack(alignment: .bottom, spacing: DesktopSpace.xs) {
                    ChipFlow(spacing: DesktopSpace.xs) { options }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button(action: draft.start) {
                        Image(systemName: draft.starting ? "ellipsis" : "arrow.up")
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundStyle(DesktopPalette.onAccent)
                            .frame(width: 32, height: 32)
                            .background(DesktopPalette.accent, in: Circle())
                    }
                    .buttonStyle(.plain)
                    .disabled(!draft.canStart)
                    .opacity(draft.canStart ? 1 : 0.45)
                    .keyboardShortcut(shortcuts.keyboardShortcut(.send))
                    .accessibilityLabel(draft.starting ? "Starting" : "Start session")
                    .help(shortcuts.help("Start the session", .send))
                }
                .padding(.horizontal, DesktopSpace.s)
                .padding(.bottom, DesktopSpace.s)
            }
            .floatingGlass(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: DesktopRadius.sheet))
            .overlay(RoundedRectangle(cornerRadius: DesktopRadius.sheet).strokeBorder(DesktopPalette.line))
            .shadow(color: .black.opacity(0.08), radius: 12, y: 6)
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
        .task(id: "\(draft.hosts.generation)/\(draft.form.agent)/\(draft.hosts.options != nil)") { await draft.loadModels() }
        .onChange(of: model.snapshot?.daemon) { draft.daemonChanged() }
        .onChange(of: draft.form.agent) { draft.error = nil }
    }

    @ViewBuilder private var options: some View {
        chip(draft.form.agent.capitalized, symbol: "sparkle", help: "Agent") {
            Picker("Agent", selection: Binding(get: { draft.form.agent }, set: { draft.form.switchAgent(to: $0) })) {
                ForEach(draft.agents, id: \.self) { Text($0.capitalized).tag($0) }
            }
            .pickerStyle(.inline)
        }
        if draft.hosts.isVisible {
            chip(draft.hostName, symbol: "desktopcomputer", help: "Choose the machine this session starts on") {
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
            folderChip
        }
        if !draft.launchers.isEmpty {
            chip(draft.form.launcher?.label ?? "Default", symbol: "terminal", help: "Where the agent runs") {
                Picker("Runs in", selection: Binding(get: { draft.form.launcher?.id }, set: { id in draft.form.selectLauncher(draft.launchers.first { $0.id == id }) })) {
                    Text("Default").tag(nil as String?)
                    ForEach(draft.launchers) { option in
                        Text(option.available ? option.label : "\(option.label) (unavailable)")
                            .tag(Optional(option.id))
                            .selectionDisabled(!option.available)
                    }
                }
                .pickerStyle(.inline)
            }
        }
        if draft.claudeConfigRoots.count > 1 {
            chip(draft.claudeConfigRoots.first { $0.choice == draft.form.configRoot }?.name ?? "Config folder", symbol: "person.crop.circle", help: "Config folder") {
                Picker("Config folder", selection: $draft.form.configRoot) {
                    ForEach(draft.claudeConfigRoots) { root in
                        Text(root.menuLabel).tag(root.choice).selectionDisabled(!root.signedIn)
                    }
                }
                .pickerStyle(.inline)
            }
        }
        if draft.form.listsModels {
            chip(modelSummary, symbol: "cpu", help: "Model") {
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
        }
    }

    @ViewBuilder private var folderChip: some View {
        if draft.startsOnThisMac {
            chip(folderLabel, symbol: "folder", help: draft.form.folder) {
                ForEach(draft.hosts.projects) { project in
                    Button(project.displayName) { draft.form.folder = project.rootPath }
                }
                Divider()
                Button("Choose…", action: chooseFolder)
            }
        } else {
            HStack(spacing: 4) {
                Image(systemName: "folder").foregroundStyle(DesktopPalette.text2)
                TextField("Folder on \(draft.hostName)", text: $draft.form.folder)
                    .textFieldStyle(.plain)
                    .frame(width: 180)
                if !draft.hosts.projects.isEmpty {
                    Menu {
                        ForEach(draft.hosts.projects) { project in Button(project.displayName) { draft.form.folder = project.rootPath } }
                    } label: { EmptyView() }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                    .accessibilityLabel("Projects on \(draft.hostName)")
                }
            }
            .font(.system(size: DesktopType.secondary))
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
        let lines = model.isConnected ? [draft.hosts.loadError, draft.modelError].compactMap { $0 } : []
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
                ForEach(lines, id: \.self) { Text($0).foregroundStyle(DesktopPalette.text2) }
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
        }
    }

    private var folderLabel: String {
        if draft.form.folder.isEmpty { return "Choose a project" }
        if let project = draft.hosts.projects.first(where: { $0.rootPath == draft.form.folder }) { return project.displayName }
        return (draft.form.folder as NSString).lastPathComponent
    }

    private var modelSummary: String {
        let chosen = draft.form.model.isEmpty ? draft.listedDefault : draft.agentModels.first { $0.id == draft.form.model }
        let name = chosen?.displayName ?? "Runtime default"
        return draft.form.effort.isEmpty ? name : "\(name) · \(draft.form.effort.capitalized)"
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

/// Lays chips out in rows that wrap at the proposed width, so the row never
/// asks the window for more width than one chip.
private struct ChipFlow: Layout {
    var spacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        arrange(subviews, width: proposal.width ?? 560).size
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        for (subview, origin) in zip(subviews, arrange(subviews, width: bounds.width).origins) {
            subview.place(at: CGPoint(x: bounds.minX + origin.x, y: bounds.minY + origin.y), proposal: .unspecified)
        }
    }

    private func arrange(_ subviews: Subviews, width: CGFloat) -> (origins: [CGPoint], size: CGSize) {
        var origins: [CGPoint] = []
        var x: CGFloat = 0, y: CGFloat = 0, row: CGFloat = 0, widest: CGFloat = 0
        for subview in subviews {
            let size = subview.sizeThatFits(.unspecified)
            if x > 0, x + size.width > width {
                x = 0
                y += row + spacing
                row = 0
            }
            origins.append(CGPoint(x: x, y: y))
            widest = max(widest, x + size.width)
            x += size.width + spacing
            row = max(row, size.height)
        }
        return (origins, CGSize(width: min(widest, width), height: y + row))
    }
}
