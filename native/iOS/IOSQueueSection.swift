import SwiftUI

struct IOSQueueSection: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let target: SessionOperationTarget
    let readOnlyReason: String
    @StateObject private var queue: NativeQueueController
    @State private var editor: IOSQueueEditor?

    init(model: AgenthailIOSModel, session: SessionState, target: SessionOperationTarget, readOnlyReason: String) {
        self.model = model
        self.session = session
        self.target = target
        self.readOnlyReason = readOnlyReason
        _queue = StateObject(wrappedValue: NativeQueueController(keys: target.keys))
    }

    private var writable: Bool { SessionOperationAvailability.nativeQueueWritable(session) && target.unavailable == nil }

    var body: some View {
        Section {
            if let unavailable = target.unavailable {
                Text(unavailable).foregroundStyle(PhonePalette.muted)
            } else if queue.loaded && queue.items.isEmpty {
                Text("Nothing queued in Codex.").foregroundStyle(PhonePalette.muted)
            } else if !queue.loaded, queue.error != nil {
                Text("Codex's queue isn't available for this session.").foregroundStyle(PhonePalette.muted)
            } else if !queue.loaded {
                Text("Loading Codex's queue").foregroundStyle(PhonePalette.muted)
            }
            ForEach(Array(queue.items.enumerated()), id: \.element.id) { index, item in
                row(item, index: index)
            }
            if queue.nextCursor != nil {
                Button("Load more") { Task { await queue.loadMore(target.api) } }
                    .disabled(queue.busy)
            }
            if writable && queue.loaded {
                Button("Add to queue", systemImage: "plus") { editor = IOSQueueEditor(item: nil, text: "") }
                    .disabled(queue.busy)
                    .accessibilityIdentifier("queue-add")
                if !queue.items.isEmpty {
                    Button("Start next", systemImage: "play") { Task { await start(nil) } }
                        .disabled(queue.busy)
                        .accessibilityIdentifier("queue-start-next")
                }
            }
            if let error = queue.error {
                Text(error).font(.footnote).foregroundStyle(PhonePalette.failed).textSelection(.enabled)
            }
        } header: {
            HStack {
                Text("Codex queue")
                Spacer()
                if queue.busy { ProgressView() }
                Button("Reload Codex queue", systemImage: "arrow.clockwise") { Task { await queue.reload(target.api) } }
                    .labelStyle(.iconOnly)
                    .disabled(queue.busy || target.api == nil)
            }
        } footer: {
            if session.isReadOnly { Label(readOnlyReason, systemImage: "lock") }
        }
        .task(id: target.identity) {
            queue.reset(sessionID: target.sessionID)
            await queue.reload(target.api)
        }
        .sheet(item: $editor) { current in
            IOSQueueEditorSheet(editor: current) { text in
                if let item = current.item { return await queue.update(item, text: text, api: target.api) }
                return await queue.add(text, api: target.api)
            }
        }
    }

    private func row(_ item: NativeQueuedSubmission, index: Int) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(item.text.isEmpty ? "Empty input" : item.text)
                .lineLimit(3)
                .frame(maxWidth: .infinity, alignment: .leading)
            if writable {
                Menu {
                    Button("Start now", systemImage: "play") { Task { await start(item) } }
                    if let text = item.editableText {
                        Button("Edit", systemImage: "pencil") { editor = IOSQueueEditor(item: item, text: text) }
                    }
                    if queue.canReorder {
                        Button("Move up", systemImage: "arrow.up") { Task { await queue.move(item, by: -1, api: target.api) } }
                            .disabled(index == 0)
                        Button("Move down", systemImage: "arrow.down") { Task { await queue.move(item, by: 1, api: target.api) } }
                            .disabled(index == queue.items.count - 1)
                    }
                    Button("Delete", systemImage: "trash", role: .destructive) { Task { await queue.delete(item, api: target.api) } }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .disabled(queue.busy)
                .accessibilityLabel("Queue item actions")
            }
        }
    }

    private func start(_ item: NativeQueuedSubmission?) async {
        if await queue.start(item, api: target.api) { await model.refreshSession(session.id) }
    }
}

struct IOSQueueEditor: Identifiable {
    let id = UUID()
    let item: NativeQueuedSubmission?
    let text: String
}

struct IOSQueueEditorSheet: View {
    let editor: IOSQueueEditor
    let save: (String) async -> Bool
    @Environment(\.dismiss) private var dismiss
    @State private var text: String
    @State private var saving = false

    init(editor: IOSQueueEditor, save: @escaping (String) async -> Bool) {
        self.editor = editor
        self.save = save
        _text = State(initialValue: editor.text)
    }

    private var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextEditor(text: $text)
                        .frame(minHeight: 120)
                        .accessibilityIdentifier("queue-editor-text")
                } footer: {
                    Text("Codex starts queued input after the current turn, in order.")
                }
            }
            .navigationTitle(editor.item == nil ? "Add to Codex queue" : "Edit queued input")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(saving ? "Saving…" : editor.item == nil ? "Add" : "Save") { commit() }
                        .disabled(saving || trimmed.isEmpty || trimmed == editor.text.trimmingCharacters(in: .whitespacesAndNewlines))
                }
            }
        }
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
