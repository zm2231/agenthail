import AppKit
import SwiftUI

struct SessionOperationsSection: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState

    var body: some View {
        if pane.removedSession == nil {
            if SessionOperationAvailability.fork(session) {
                Button("Fork session…") { pane.forkingSession = session }
                    .controlSize(.small)
                    .help("Start a new Codex session from this conversation's history")
            }
            if SessionOperationAvailability.nativeQueue(session) {
                NativeQueueSection(model: model, session: session)
            }
            if SessionOperationAvailability.lifecycle(session) {
                BackgroundSessionSection(model: model, session: session)
            }
        }
    }
}

struct NativeQueueSection: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState
    @StateObject private var queue = NativeQueueController()
    @State private var editor: QueueEditor?

    private var writable: Bool { SessionOperationAvailability.nativeQueueWritable(session) }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                SidebarCaption("Codex queue").padding(.horizontal, -8)
                Spacer()
                if queue.busy { ProgressView().controlSize(.mini) }
                Button {
                    Task { await queue.reload(model.api) }
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .disabled(queue.busy)
                .help("Reload Codex's queue")
                .accessibilityLabel("Reload Codex queue")
            }
            if queue.loaded && queue.items.isEmpty {
                Text("Nothing queued in Codex.")
                    .foregroundStyle(DesktopPalette.text2)
            } else if !queue.loaded, let error = queue.error {
                Text("Codex's queue isn't available for this session.")
                    .foregroundStyle(DesktopPalette.text2)
                    .help(error)
            }
            ForEach(Array(queue.items.enumerated()), id: \.element.id) { index, item in
                row(item, index: index)
            }
            if queue.nextCursor != nil {
                Button("Load more") { Task { await queue.loadMore(model.api) } }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accentText)
                    .disabled(queue.busy)
            }
            if writable && queue.loaded {
                HStack(spacing: 8) {
                    Button("Add…") { editor = QueueEditor(item: nil, text: "") }
                    if !queue.items.isEmpty {
                        Button("Start next") { Task { await start(nil) } }
                            .help("Let Codex start its next queued input")
                    }
                }
                .controlSize(.small)
                .disabled(queue.busy)
            }
            if queue.loaded, let error = queue.error {
                Text(error)
                    .font(.system(size: 11.5))
                    .foregroundStyle(DesktopPalette.red)
                    .textSelection(.enabled)
            }
        }
        .task(id: session.id) {
            queue.reset(sessionID: session.id)
            await queue.reload(model.api)
        }
        .sheet(item: $editor) { current in
            QueueEditorSheet(editor: current) { text in
                if let item = current.item { return await queue.update(item, text: text, api: model.api) }
                return await queue.add(text, api: model.api)
            }
        }
    }

    private func row(_ item: NativeQueuedSubmission, index: Int) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(item.text.isEmpty ? "Empty input" : item.text)
                .lineLimit(3)
                .frame(maxWidth: .infinity, alignment: .leading)
                .textSelection(.enabled)
            if writable {
                Menu {
                    Button("Start now") { Task { await start(item) } }
                    if let text = item.editableText {
                        Button("Edit…") { editor = QueueEditor(item: item, text: text) }
                    }
                    if queue.canReorder {
                        Divider()
                        Button("Move up") { Task { await queue.move(item, by: -1, api: model.api) } }
                            .disabled(index == 0)
                        Button("Move down") { Task { await queue.move(item, by: 1, api: model.api) } }
                            .disabled(index == queue.items.count - 1)
                    }
                    Divider()
                    Button("Delete", role: .destructive) { Task { await queue.delete(item, api: model.api) } }
                } label: {
                    Image(systemName: "ellipsis")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .disabled(queue.busy)
                .accessibilityLabel("Queue item actions")
            }
        }
    }

    private func start(_ item: NativeQueuedSubmission?) async {
        if await queue.start(item, api: model.api) { _ = await model.refresh(fresh: true) }
    }
}

struct QueueEditor: Identifiable {
    let id = UUID()
    let item: NativeQueuedSubmission?
    let text: String
}

struct QueueEditorSheet: View {
    let editor: QueueEditor
    let save: (String) async -> Bool
    @Environment(\.dismiss) private var dismiss
    @State private var text: String
    @State private var saving = false

    init(editor: QueueEditor, save: @escaping (String) async -> Bool) {
        self.editor = editor
        self.save = save
        _text = State(initialValue: editor.text)
    }

    private var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(editor.item == nil ? "Add to Codex queue" : "Edit queued input")
                .font(.headline)
            Text("Codex starts queued input after the current turn, in order.")
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
            TextEditor(text: $text)
                .font(.body)
                .frame(minHeight: 100)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(DesktopPalette.line))
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(saving ? "Saving…" : editor.item == nil ? "Add" : "Save") { commit() }
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(saving || trimmed.isEmpty || trimmed == editor.text.trimmingCharacters(in: .whitespacesAndNewlines))
            }
        }
        .padding(20)
        .frame(width: 400)
    }

    private func commit() {
        saving = true
        Task {
            let succeeded = await save(trimmed)
            saving = false
            if succeeded { dismiss() }
        }
    }
}

struct BackgroundSessionSection: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState
    @StateObject private var controller = BackgroundSessionController()

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                SidebarCaption("Background session").padding(.horizontal, -8)
                Spacer()
                if controller.busyAction != nil { ProgressView().controlSize(.mini) }
            }
            if let status = controller.status {
                Text([status.state.map { "State: \($0)" }, status.id.map { "Job \($0)" }].compactMap { $0 }.joined(separator: " · "))
                    .textSelection(.enabled)
                if controller.resumeUnchanged {
                    Text("Already running, nothing to resume.")
                        .font(.system(size: 11.5))
                        .foregroundStyle(DesktopPalette.text2)
                }
            } else {
                Text("For Claude sessions running in the background.")
                    .font(.system(size: 11.5))
                    .foregroundStyle(DesktopPalette.text2)
            }
            HStack(spacing: 8) {
                Button("Status") { run("status") }
                Button("Logs") { run("logs") }
                Button("Stop") { run("stop") }
                Button("Resume") { run("resume") }
            }
            .controlSize(.small)
            .disabled(controller.busyAction != nil)
            if let error = controller.error {
                Text(error)
                    .font(.system(size: 11.5))
                    .foregroundStyle(DesktopPalette.red)
                    .textSelection(.enabled)
            }
        }
        .task(id: session.id) { controller.reset(sessionID: session.id) }
        .sheet(isPresented: Binding(get: { controller.logs != nil }, set: { if !$0 { controller.logs = nil } })) {
            BackgroundLogsSheet(title: session.title, logs: controller.logs ?? "")
        }
    }

    private func run(_ action: String) {
        Task {
            if await controller.run(action, api: model.api), action == "stop" || action == "resume" {
                _ = await model.refresh(fresh: true)
            }
        }
    }
}

struct BackgroundLogsSheet: View {
    let title: String
    let logs: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Logs for \(title)")
                .font(.headline)
            ScrollView {
                Text(logs.isEmpty ? "No log output." : logs)
                    .font(.system(size: 11.5, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
            }
            .background(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).stroke(DesktopPalette.line))
            HStack {
                Button("Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(logs, forType: .string)
                }
                Spacer()
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 560, height: 420)
    }
}

struct ForkSessionSheet: View {
    @ObservedObject var pane: SessionPane
    let session: SessionState
    @Environment(\.dismiss) private var dismiss
    @State private var folder = ""
    @State private var forking = false
    @State private var error: String?
    @State private var idempotencyKey = UUID().uuidString

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Fork \(session.title)")
                .font(.headline)
            Text("Codex starts a new session with this conversation's history. The original keeps running. Forking doesn't create a Git worktree.")
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                TextField("Folder (optional, defaults to the original's)", text: $folder)
                    .textFieldStyle(.roundedBorder)
                Button("Choose…", action: chooseFolder)
            }
            if let error {
                Text(error)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.red)
                    .textSelection(.enabled)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(forking ? "Forking…" : "Fork", action: fork)
                    .keyboardShortcut(.defaultAction)
                    .disabled(forking)
            }
        }
        .padding(20)
        .frame(width: 440)
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.prompt = "Choose"
        if let cwd = session.cwd { panel.directoryURL = URL(fileURLWithPath: cwd) }
        if panel.runModal() == .OK, let url = panel.url { folder = url.path }
    }

    private func fork() {
        forking = true
        error = nil
        let cwd = folder.trimmingCharacters(in: .whitespacesAndNewlines)
        Task {
            let failure = await pane.fork(session, cwd: cwd.isEmpty ? nil : (cwd as NSString).expandingTildeInPath, idempotencyKey: idempotencyKey)
            forking = false
            if let failure { error = failure } else { dismiss() }
        }
    }
}
