import AppKit
import SwiftUI

struct NewSessionSheet: View {
    @ObservedObject var model: AgenthailModel
    @Environment(\.dismiss) private var dismiss
    @State private var agent = "claude"
    @State private var launcher: String?
    @State private var folder = ""
    @State private var message = ""
    @State private var starting = false
    @State private var error: String?

    private var launchers: [LauncherOption] {
        (model.creationOptions?.launchers ?? []).filter { $0.agents.contains(agent) }
    }

    private var agents: [String] {
        let supported = (model.creationOptions?.surfaces ?? []).filter(\.workspace).map(\.id)
        return ["claude", "codex"].filter { supported.isEmpty || supported.contains($0) }
    }

    private var folders: [String] {
        var seen = Set<String>()
        return model.knownSessions
            .filter { $0.hostProject != nil }
            .sorted { SessionTree.activity($0) > SessionTree.activity($1) }
            .compactMap { $0.checkout?.path ?? $0.cwd }
            .filter { !$0.hasPrefix("/private/") && !$0.hasPrefix("/var/") && !$0.contains("/.no-mistakes/") && seen.insert($0).inserted }
            .prefix(12)
            .map { $0 }
    }

    private var canStart: Bool {
        !starting && !folder.isEmpty && !message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && (launchers.isEmpty || launchers.contains { $0.id == launcher && $0.available })
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("New session")
                .font(.system(size: 15, weight: .semibold))
            Form {
                Picker("Agent", selection: $agent) {
                    ForEach(agents, id: \.self) { Text($0.capitalized).tag($0) }
                }
                if !launchers.isEmpty {
                    Picker("Runs in", selection: $launcher) {
                        ForEach(launchers) { option in
                            Text(option.available ? option.label : "\(option.label) (unavailable)")
                                .tag(Optional(option.id))
                                .help(option.detail ?? "")
                        }
                    }
                }
                HStack {
                    Picker("Folder", selection: $folder) {
                        if folder.isEmpty { Text("Choose a folder").tag("") }
                        ForEach(folders, id: \.self) { path in
                            Text((path as NSString).abbreviatingWithTildeInPath).tag(path)
                        }
                        if !folder.isEmpty && !folders.contains(folder) {
                            Text((folder as NSString).abbreviatingWithTildeInPath).tag(folder)
                        }
                    }
                    Button("Choose…", action: chooseFolder)
                }
                TextField("First message", text: $message, axis: .vertical)
                    .lineLimit(3...8)
            }
            .formStyle(.grouped)
            if let error {
                Text(error)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.red)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(starting ? "Starting…" : "Start") { start() }
                    .keyboardShortcut(.return, modifiers: .command)
                    .buttonStyle(.borderedProminent)
                    .disabled(!canStart)
            }
        }
        .padding(20)
        .frame(width: 520)
        .task {
            await model.loadCreationOptions()
            if folder.isEmpty { folder = model.selectedSession.flatMap { $0.checkout?.path ?? $0.cwd } ?? folders.first ?? "" }
            selectDefaultLauncher()
        }
        .onChange(of: agent) { selectDefaultLauncher() }
    }

    private func selectDefaultLauncher() {
        if let launcher, launchers.contains(where: { $0.id == launcher && $0.available }) { return }
        launcher = launchers.first(where: \.available)?.id
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        if !folder.isEmpty { panel.directoryURL = URL(fileURLWithPath: folder) }
        if panel.runModal() == .OK, let url = panel.url { folder = url.path }
    }

    private func start() {
        starting = true
        error = nil
        let text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        Task {
            let failure = await model.launchSession(launcher: launchers.isEmpty ? nil : launcher, agent: agent, folder: folder, message: text)
            starting = false
            if let failure { error = failure } else { dismiss() }
        }
    }
}
