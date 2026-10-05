import SwiftUI

struct SessionEditingControls: View {
    @ObservedObject var model: AgenthailIOSModel
    let detail: SessionDetail
    @State private var editingGoal = false
    @State private var editingBudget = false
    @State private var editingName = false
    @State private var text = ""
    @State private var budgetText = ""
    @State private var error: String?
    var body: some View {
        Section("Organize conversation") {
            Button("Name conversation", systemImage: "pencil") { text = detail.alias ?? ""; editingName = true }
            if !detail.readOnly && detail.capabilities.goal {
                Button(detail.goal?.objective.isEmpty == false ? "Edit goal" : "Set goal", systemImage: "target") { text = detail.goal?.objective ?? ""; editingGoal = true }
                if detail.goal?.objective.isEmpty == false { Button("Clear goal", role: .destructive) { Task { await save("goal-clear") } } }
                if let goal = detail.goal {
                    if goal.status == "active" {
                        Button("Pause goal", systemImage: "pause.fill") { Task { await save("goal-pause") } }
                    } else if goal.status == "paused" {
                        Button("Resume goal", systemImage: "play.fill") { Task { await save("goal-resume") } }
                    }
                    Button(goal.tokenBudget == nil ? "Set token budget" : "Edit token budget", systemImage: "gauge.with.dots.needle.67percent") {
                        budgetText = goal.tokenBudget.map(String.init) ?? ""
                        editingBudget = true
                    }
                    if goal.tokenBudget != nil {
                        Button("Clear token budget", role: .destructive) { Task { await save("goal-budget", text: "") } }
                    }
                }
            }
            if let error { Text(error).font(.footnote).foregroundStyle(.red) }
        }
        .disabled(model.pendingControls.contains(detail.session.id))
        .sheet(isPresented: Binding(get: { editingGoal || editingName || editingBudget }, set: { if !$0 { editingGoal = false; editingName = false; editingBudget = false } })) {
            NavigationStack {
                Form {
                    if editingBudget {
                        TextField("Token budget", text: $budgetText).keyboardType(.numberPad)
                            .textInputAutocapitalization(.never).autocorrectionDisabled()
                        Text("Use Clear token budget to remove the current limit.").font(.footnote)
                    } else {
                        TextField(editingName ? "Name" : "Objective", text: $text, axis: .vertical).lineLimit(2...8)
                            .textInputAutocapitalization(editingName ? .never : .sentences).autocorrectionDisabled(editingName)
                    }
                    if editingName { Text("Use 1–80 characters without spaces, /, or #.").font(.footnote) }
                    if let error { Text(error).foregroundStyle(.red) }
                }
                .navigationTitle(editingBudget ? "Token budget" : editingName ? "Name conversation" : "Edit goal")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { editingName = false; editingGoal = false; editingBudget = false }.disabled(model.pendingControls.contains(detail.session.id)) }
                    ToolbarItem(placement: .confirmationAction) { Button("Save") { Task { await save(editingBudget ? "goal-budget" : editingName ? "alias" : editingGoal && detail.goal?.objective.isEmpty == false ? "goal-edit" : "goal-set", text: editingBudget ? budgetText : text) } }.disabled((editingBudget ? budgetText : text).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.pendingControls.contains(detail.session.id)) }
                }.interactiveDismissDisabled(model.pendingControls.contains(detail.session.id))
            }
        }
    }
    private func save(_ action: String, text: String? = nil) async {
        error = nil
        do { try await model.editSession(id: detail.session.id, action: action, text: text ?? self.text); editingName = false; editingGoal = false; editingBudget = false }
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
    private var current: [QueueState] { items.filter { !$0.isHistorical } }
    private var history: [QueueState] { items.filter { $0.isHistorical } }

    var body: some View {
        List {
            Picker("Inbox scope", selection: $showHistory) {
                Text("Current").tag(false)
                Text("History").tag(true)
            }.pickerStyle(.segmented).listRowSeparator(.hidden)
            if let error { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(.red).font(.footnote) }
            if let error = model.connectionError { Label(error, systemImage: "wifi.exclamationmark").foregroundStyle(.secondary).font(.footnote) }
            let problems = model.deliveryProblems.filter { sessionID == nil || $0.sessionId == sessionID || $0.sourceSessionId == sessionID }
            if !problems.isEmpty {
                Section("Delivery problems") {
                    ForEach(problems) { problem in
                        deliveryProblemRow(problem)
                    }
                }
            }
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
            Text(retryCandidate?.evidence == "unknown" && retryCandidate?.isHistorical == true ? "This delivery outcome was never confirmed and the queue entry later expired. Check the session before sending again." : retryCandidate?.status == "expired" ? "This instruction expired without being sent. Sending again creates a new delivery attempt." : "The previous attempt may already have reached the agent. Send again only after checking the session.")
        }
    }

    private func queueRow(_ item: QueueState) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text(item.isHistorical && item.evidence == "unknown" ? "Outcome unknown · expired" : item.isHistorical && item.evidence == "failed" ? "Delivery failed · expired" : item.evidence == "transport_accepted" ? "Transport accepted" : item.status == "dead" ? "Delivery needs review" : item.status == "expired" ? "Expired" : item.status == "inflight" ? "Sending" : item.status.capitalized)
                    .font(.subheadline.weight(.semibold)).foregroundStyle(item.status == "dead" && !item.isHistorical ? .orange : .secondary)
                Spacer()
                Text(item.queuedAt).font(.footnote).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
            Button {
                if let onOpenSession { onOpenSession(item.sessionId) }
                else { dismiss(); model.openSession(item.sessionId) }
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
                if let value = item.sourceSessionId { LabeledContent("From session", value: value).font(.footnote).textSelection(.enabled) }
            }.font(.footnote)
            HStack {
                if item.status == "dead" || item.status == "expired" {
                    Button("Send again") { retryCandidate = item }.buttonStyle(.bordered)
                }
                if item.status == "pending" || (item.status == "dead" && !item.isHistorical) {
                    Button(item.status == "dead" ? "Dismiss" : "Cancel delivery", role: .destructive) { update(item, retry: false) }.buttonStyle(.bordered)
                }
            }
            .controlSize(.large)
            .disabled(model.pendingControls.contains("queue:\(item.id)"))
        }.padding(.vertical, 6)
    }

    private func deliveryProblemRow(_ problem: DeliveryProblem) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline) {
                Text(problem.status?.capitalized ?? "Delivery problem")
                    .font(.subheadline.weight(.semibold))
                Spacer()
                Text(problem.at).font(.footnote).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
            Text(problem.message).font(.body).textSelection(.enabled)
            Text(problem.reason).font(.footnote).foregroundStyle(.secondary).textSelection(.enabled)
            Button("Dismiss", role: .destructive) {
                Task {
                    error = nil
                    do { try await model.dismissDeliveryProblem(problem) }
                    catch { self.error = error.localizedDescription }
                }
            }
            .buttonStyle(.bordered)
            .disabled(model.pendingControls.contains("delivery:\(problem.deliveryId)"))
        }
        .padding(.vertical, 6)
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
