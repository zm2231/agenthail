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
    var onOpenSession: ((String) -> Void)? = nil
    @Environment(\.dismiss) private var dismiss
    @State private var error: String?
    @State private var queue: [QueueState] = []
    @State private var loading = true
    @State private var showHistory = false
    @State private var retryCandidate: QueueState?
    @State private var reloadID = UUID()

    private var items: [QueueState] { queue.filter { sessionID == nil || $0.sessionId == sessionID } }
    private var current: [QueueState] { items.filter { $0.status != "expired" && $0.status != "delivered" && $0.status != "canceled" } }
    private var history: [QueueState] { items.filter { $0.status == "expired" || $0.status == "delivered" || $0.status == "canceled" } }

    var body: some View {
        List {
            Picker("Inbox scope", selection: $showHistory) {
                Text("Current").tag(false)
                Text("History").tag(true)
            }.pickerStyle(.segmented).listRowSeparator(.hidden)
            if let error { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.red).font(.footnote) }
            if let error = model.connectionError { Label(error, systemImage: "wifi.exclamationmark").foregroundStyle(.secondary).font(.footnote) }
            if loading && items.isEmpty { ProgressView("Loading deliveries") }
            if !showHistory {
                let review = current.filter { $0.status == "dead" }
                let pending = current.filter { $0.status != "dead" }
                if !review.isEmpty {
                    Section {
                        ForEach(review) { item in queueRow(item) }
                    } header: { Text("Review delivery") }
                    footer: { Text("Unconfirmed instructions may already have reached the agent. Check the session before sending again.") }
                }
                if !pending.isEmpty {
                    Section("Waiting to deliver") { ForEach(pending) { item in queueRow(item) } }
                }
                if !loading && error == nil && model.connectionError == nil && current.isEmpty {
                    ContentUnavailableView("No pending deliveries", systemImage: "tray", description: Text("Instructions waiting to send or needing a delivery decision appear here. Expired instructions are in History."))
                        .listRowSeparator(.hidden)
                }
            } else {
                if history.isEmpty && !loading {
                    ContentUnavailableView("No delivery history", systemImage: "clock", description: Text("Expired and completed instructions appear here."))
                        .listRowSeparator(.hidden)
                }
                ForEach(history.reversed()) { item in queueRow(item) }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(sessionID == nil ? "Inbox" : "Session inbox")
        .task { await reload() }
        .refreshable { _ = await model.refresh(fresh: true); await reload() }
        .onChange(of: model.snapshot?.updatedAt) { _, _ in Task { await reload() } }
        .confirmationDialog("Send this instruction again?", isPresented: Binding(get: { retryCandidate != nil }, set: { if !$0 { retryCandidate = nil } }), titleVisibility: .visible) {
            if let item = retryCandidate {
                Button("Send again") { update(item, retry: true); retryCandidate = nil }
                Button("Cancel", role: .cancel) { retryCandidate = nil }
            }
        } message: {
            Text(retryCandidate?.status == "expired" ? "This instruction expired without being sent. Sending again creates a new delivery attempt." : "The previous attempt may already have reached the agent. Send again only after checking the session.")
        }
    }

    private func queueRow(_ item: QueueState) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text(item.status == "dead" ? "Delivery needs review" : item.status == "expired" ? "Expired" : item.status == "inflight" ? "Sending" : item.status.capitalized)
                    .font(.caption.weight(.semibold)).foregroundStyle(item.status == "dead" ? .orange : .secondary)
                Spacer()
                Text(item.queuedAt).font(.caption2).foregroundStyle(.secondary)
            }
            Button {
                if let onOpenSession { onOpenSession(item.sessionId) }
                else { dismiss(); model.openNotification(item.sessionId) }
            } label: {
                HStack {
                    Text(item.target).font(.headline)
                    Spacer(minLength: 8)
                    Image(systemName: "chevron.right").font(.caption)
                }.frame(minHeight: 44, alignment: .leading)
            }.buttonStyle(.plain).accessibilityIdentifier("inbox-session-\(item.id)")
            Text(item.message).font(.body).lineLimit(4).textSelection(.enabled)
            if let reason = item.lastError, !reason.isEmpty {
                Text(reason).font(.footnote).foregroundStyle(.secondary)
            }
            DisclosureGroup("Instruction details") {
                Text(item.message).textSelection(.enabled)
                LabeledContent("Attempts", value: item.attempts.formatted()).font(.footnote)
                if let value = item.model { LabeledContent("Model", value: value).font(.footnote) }
                if let value = item.effort { LabeledContent("Effort", value: value).font(.footnote) }
                if let value = item.mode { LabeledContent("Mode", value: value).font(.footnote) }
                if let value = item.serviceTier { LabeledContent("Service tier", value: value).font(.footnote) }
                if let value = item.sourceSessionId { LabeledContent("From session", value: value).font(.footnote).textSelection(.enabled) }
                if let schema = item.outputSchema { TranscriptCode(text: schema.formatted) }
            }.font(.footnote)
            HStack {
                if item.status == "dead" || item.status == "expired" {
                    Button("Send again") { retryCandidate = item }.buttonStyle(.bordered)
                }
                if item.status == "pending" || item.status == "dead" {
                    Button(item.status == "dead" ? "Dismiss" : "Cancel delivery", role: .destructive) { update(item, retry: false) }.buttonStyle(.bordered)
                }
            }
            .controlSize(.large)
            .disabled(model.pendingControls.contains("queue:\(item.id)"))
        }.padding(.vertical, 6)
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
