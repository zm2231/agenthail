import SwiftUI

struct SessionEditingControls: View {
    @ObservedObject var model: AgenthailIOSModel
    let detail: SessionDetail
    @State private var editingGoal = false
    @State private var editingName = false
    @State private var text = ""
    @State private var error: String?
    var body: some View {
        Section("Organize conversation") {
            Button("Name conversation", systemImage: "pencil") { text = detail.alias ?? ""; editingName = true }
            if !detail.readOnly && detail.capabilities.goal {
                Button(detail.goal?.objective.isEmpty == false ? "Edit goal" : "Set goal", systemImage: "target") { text = detail.goal?.objective ?? ""; editingGoal = true }
                if detail.goal?.objective.isEmpty == false {
                    Button("Clear goal", role: .destructive) { Task { await save("goal-clear") } }
                }
            }
            if let error { Text(error).font(.footnote).foregroundStyle(.red) }
        }
        .disabled(model.pendingControls.contains(detail.session.id))
        .sheet(isPresented: Binding(get: { editingGoal || editingName }, set: { if !$0 { editingGoal = false; editingName = false } })) {
            NavigationStack {
                Form {
                    TextField(editingName ? "Name" : "Objective", text: $text, axis: .vertical).lineLimit(2...8)
                        .textInputAutocapitalization(editingName ? .never : .sentences).autocorrectionDisabled(editingName)
                    if editingName { Text("Use 1–80 characters without spaces, /, or #.").font(.footnote) }
                    if let error { Text(error).foregroundStyle(.red) }
                }
                .navigationTitle(editingName ? "Name conversation" : "Edit goal")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { editingName = false; editingGoal = false }.disabled(model.pendingControls.contains(detail.session.id)) }
                    ToolbarItem(placement: .confirmationAction) { Button("Save") { Task { await save(editingName ? "alias" : "goal-set") } }.disabled(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.pendingControls.contains(detail.session.id)) }
                }.interactiveDismissDisabled(model.pendingControls.contains(detail.session.id))
            }
        }
    }
    private func save(_ action: String) async {
        error = nil
        do { try await model.editSession(id: detail.session.id, action: action, text: text); editingName = false; editingGoal = false }
        catch { self.error = error.localizedDescription }
    }
}

struct QueueListView: View {
    @ObservedObject var model: AgenthailIOSModel
    var sessionID: String? = nil
    @State private var error: String?
    @State private var queue: [QueueState] = []
    @State private var loading = true
    @State private var reloadID = UUID()
    private var items: [QueueState] { queue.filter { sessionID == nil || $0.sessionId == sessionID } }
    var body: some View {
        List {
            if let error { Text(error).foregroundStyle(.red) }
            if loading { ProgressView("Loading queued instructions") }
            if !loading && error == nil && items.isEmpty { ContentUnavailableView("No queued instructions", systemImage: "tray") }
            ForEach(items) { item in
                VStack(alignment: .leading, spacing: 10) {
                    NavigationLink { SessionRouteView(model: model, sessionID: item.sessionId) } label: { Text(item.target).fontWeight(.semibold) }
                    Text(item.message).textSelection(.enabled)
                    Text(item.status.capitalized).font(.caption).foregroundStyle(.secondary)
                    if let reason = item.lastError, !reason.isEmpty { Text(reason).font(.footnote).foregroundStyle(.red) }
                    HStack {
                        if item.status == "dead" || item.status == "expired" { Button("Retry") { update(item, retry: true) }.buttonStyle(.bordered) }
                        if item.status == "pending" || item.status == "dead" { Button("Cancel instruction", role: .destructive) { update(item, retry: false) }.buttonStyle(.bordered) }
                    }.disabled(model.pendingControls.contains("queue:\(item.id)"))
                }.padding(.vertical, 4)
            }
        }.navigationTitle("Queued instructions")
        .task { await reload() }
        .refreshable { await reload() }
        .onChange(of: model.snapshot?.updatedAt) { _, _ in Task { await reload() } }
    }
    private func update(_ item: QueueState, retry: Bool) {
        error = nil
        Task { do { try await model.updateQueue(item, retry: retry); await reload() } catch { self.error = error.localizedDescription } }
    }
    private func reload() async {
        let requestID = UUID(); reloadID = requestID
        do {
            let items = try await model.queuedInstructions()
            guard reloadID == requestID, !Task.isCancelled else { return }
            queue = items; error = nil
        } catch {
            guard reloadID == requestID, !Task.isCancelled else { return }
            self.error = error.localizedDescription
        }
        loading = false
    }
}
