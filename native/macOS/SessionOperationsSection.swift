import AppKit
import SwiftUI

struct SessionOperationsSection: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState

    var body: some View {
        if pane.removedSession == nil {
            if SessionOperationAvailability.fork(session) {
                Button(pane.forking ? "Forking…" : "Fork session") { pane.fork(session) }
                    .controlSize(.small)
                    .disabled(pane.forking)
                    .help("Start a new Codex session from this conversation's history")
            }
            if SessionOperationAvailability.nativeQueue(session) {
                NativeQueueSection(model: model, session: session)
            }
            let lifecycle = SessionOperationAvailability.lifecycle(session)
            if lifecycle.any {
                BackgroundSessionSection(model: model, session: session, lifecycle: lifecycle)
            }
        }
    }
}

struct NativeQueueSection: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState
    @StateObject private var queue: NativeQueueController
    @State private var editor: QueueEditor?

    init(model: AgenthailModel, session: SessionState) {
        self.model = model
        self.session = session
        _queue = StateObject(wrappedValue: NativeQueueController(keys: model.actionKeys))
    }

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
                HStack(spacing: DesktopSpace.s) {
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
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.failed)
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
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text(editor.item == nil ? "Add to Codex queue" : "Edit queued input")
                .font(.headline)
            Text("Codex starts queued input after the current turn, in order.")
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
            TextEditor(text: $text)
                .font(.body)
                .frame(minHeight: 100)
                .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control).stroke(DesktopPalette.line))
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(saving ? "Saving…" : editor.item == nil ? "Add" : "Save") { commit() }
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(saving || trimmed.isEmpty || trimmed == editor.text.trimmingCharacters(in: .whitespacesAndNewlines))
            }
        }
        .padding(DesktopSpace.xl)
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
    let lifecycle: SessionLifecycle
    @StateObject private var controller: BackgroundSessionController

    init(model: AgenthailModel, session: SessionState, lifecycle: SessionLifecycle) {
        self.model = model
        self.session = session
        self.lifecycle = lifecycle
        _controller = StateObject(wrappedValue: BackgroundSessionController(keys: model.actionKeys))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                SidebarCaption(SessionLifecycleCopy.title(session)).padding(.horizontal, -8)
                Spacer()
                if controller.busyAction != nil { ProgressView().controlSize(.mini) }
            }
            if let status = controller.status {
                Text([status.state.map { "State: \($0)" }, status.id.map { "Job \($0)" }].compactMap { $0 }.joined(separator: " · "))
                    .textSelection(.enabled)
                if controller.resumeUnchanged {
                    Text("Already running, nothing to resume.")
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                }
            } else {
                Text(SessionLifecycleCopy.detail(session))
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.text2)
            }
            HStack(spacing: DesktopSpace.s) {
                if lifecycle.status { Button("Status") { run("status") } }
                if lifecycle.logs { Button("Logs") { run("logs") } }
                if lifecycle.stop { Button("Stop") { run("stop") } }
                if lifecycle.resume { Button("Resume") { run("resume") } }
            }
            .controlSize(.small)
            .disabled(controller.busyAction != nil)
            if let error = controller.error {
                Text(error)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.failed)
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
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text("Logs for \(title)")
                .font(.headline)
            ScrollView {
                Text(logs.isEmpty ? "No log output." : logs)
                    .font(.system(size: DesktopType.meta, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(DesktopSpace.s)
            }
            .background(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
            .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control).stroke(DesktopPalette.line))
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
        .padding(DesktopSpace.xl)
        .frame(width: 560, height: 420)
    }
}
