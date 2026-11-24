import SwiftUI

enum SessionEdit: Identifiable {
    case name, goal, budget
    var id: Self { self }
}

struct SessionEditingControls: View {
    var model: AgenthailIOSModel
    let detail: SessionDetail
    let edit: (SessionEdit) -> Void
    @State private var error: String?
    var body: some View {
        Section("Organize conversation") {
            Button("Name conversation", systemImage: "pencil") { edit(.name) }
            let goal = GoalControls(detail)
            if goal.offers(.setGoal) || goal.offers(.editGoal) {
                Button(goal.offers(.editGoal) ? "Edit goal" : "Set goal", systemImage: "target") { edit(.goal) }
            }
            if goal.offers(.clearGoal) { Button("Clear goal", role: .destructive) { Task { await save("goal-clear") } } }
            if goal.offers(.pause) { Button("Pause goal", systemImage: "pause.fill") { Task { await save("goal-pause") } } }
            if goal.offers(.resume) { Button("Resume goal", systemImage: "play.fill") { Task { await save("goal-resume") } } }
            if goal.offers(.setBudget) || goal.offers(.editBudget) {
                Button(goal.offers(.editBudget) ? "Edit token budget" : "Set token budget", systemImage: "gauge.with.dots.needle.67percent") { edit(.budget) }
            }
            if goal.offers(.clearBudget) { Button("Clear token budget", role: .destructive) { Task { await save("goal-budget") } } }
            if let error { Text(error).font(.footnote).foregroundStyle(PhonePalette.failed) }
        }
        .disabled(model.pendingControls.contains(detail.session.id))
    }

    private func save(_ action: String) async {
        error = nil
        do { try await model.editSession(id: detail.session.id, action: action) }
        catch { self.error = error.localizedDescription }
    }
}

struct SessionGoalSheet: View {
    var model: AgenthailIOSModel
    let detail: SessionDetail
    let budget: Bool
    let close: () -> Void
    @State private var text: String
    @State private var error: String?

    init(model: AgenthailIOSModel, detail: SessionDetail, budget: Bool, close: @escaping () -> Void) {
        self.model = model
        self.detail = detail
        self.budget = budget
        self.close = close
        _text = State(initialValue: Self.saved(detail, budget: budget))
    }

    private static func saved(_ detail: SessionDetail, budget: Bool) -> String {
        budget ? detail.goal?.tokenBudget.map(String.init) ?? "" : detail.goal?.objective ?? ""
    }

    private var pending: Bool { model.pendingControls.contains(detail.session.id) }
    private var action: String {
        if budget { return "goal-budget" }
        return detail.goal?.objective.isEmpty == false ? "goal-edit" : "goal-set"
    }
    private var canSave: Bool { !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View { content.holdsDraft(text != Self.saved(detail, budget: budget)) }

    private var content: some View {
        NavigationStack {
            Form {
                if budget {
                    TextField("Token budget", text: $text).keyboardType(.numberPad)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    Text("Use Clear token budget to remove the current limit.").font(.footnote)
                } else {
                    TextField("Objective", text: $text, axis: .vertical).lineLimit(2...8)
                        .textInputAutocapitalization(.sentences)
                }
                if let error { Text(error).foregroundStyle(PhonePalette.failed) }
            }
            .navigationTitle(budget ? "Token budget" : "Edit goal")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { close() }.disabled(pending) }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { Task { await save() } }
                        .disabled(!canSave || pending)
                }
            }
            .interactiveDismissDisabled(pending)
        }
    }

    private func save() async {
        error = nil
        do {
            try await model.editSession(id: detail.session.id, action: action, text: text)
            close()
        } catch {
            self.error = error.localizedDescription
        }
    }
}

struct QueueListView: View {
    var model: AgenthailIOSModel
    @Environment(\.scenePhase) private var scenePhase
    @State private var error: String?
    @State private var page = InboxPage()
    @State private var loading = true
    @State private var settled = false
    @State private var loadingOlder = false
    @State private var showHistory = false
    @State private var reloadID = UUID()
    @State private var answering: DecisionItem?

    private var current: [QueueState] { page.active }
    private var history: [InboxMessage] { page.history }
    private var pollsHistory: Bool { showHistory && scenePhase == .active }
    private var activeInSnapshot: [String] {
        model.queue.map { "\($0.id):\($0.status)" }
    }
    private var waitingInSnapshot: [String] { model.waitingSessions }

    var body: some View {
        List {
            Group {
                Picker("Inbox scope", selection: $showHistory) {
                    Text("Current").tag(false)
                    Text("History").tag(true)
                }.pickerStyle(.segmented).listRowSeparator(.hidden)
                if let error { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(PhonePalette.failed).font(.footnote) }
                if let error = model.connectionError { Label(error, systemImage: "wifi.exclamationmark").foregroundStyle(PhonePalette.muted).font(.footnote) }
                if !page.unreachable.isEmpty {
                    Label("Couldn’t reach \(page.unreachable.formatted(.list(type: .and))). Its deliveries aren’t shown.", systemImage: "wifi.exclamationmark")
                        .foregroundStyle(PhonePalette.muted).font(.footnote)
                        .accessibilityIdentifier("inbox-unreachable")
                }
                ForEach(page.outdated, id: \.self) { name in
                    Label("Update Agenthail on \(name) to see its messages here.", systemImage: "arrow.up.circle")
                        .foregroundStyle(PhonePalette.muted).font(.footnote)
                        .accessibilityIdentifier("inbox-outdated-\(name)")
                }
                if !model.leadReplacements.isEmpty {
                    Section("Lead replaced") {
                        ForEach(model.leadReplacements, id: \.listID) { replacement in
                            leadReplacementRow(replacement)
                        }
                    }
                }
                if loading && page.isEmpty { ProgressView("Loading deliveries") }
                if !showHistory {
                    DecisionSections(model: model, inbox: model.shownDecisions, open: open) { answering = $0 }
                    let review = current.filter { $0.status == "dead" }
                    let pending = current.filter { $0.status != "dead" }
                    if !review.isEmpty {
                        Section {
                            ForEach(review, id: \.listID) { item in queueRow(item) }
                        } header: { Text("Review delivery") }
                        footer: { Text("Unconfirmed instructions may already have reached the agent. Check the session before you answer what to do with one.") }
                    }
                    if !pending.isEmpty {
                        Section("Waiting to deliver") { ForEach(pending, id: \.listID) { item in queueRow(item) } }
                    }
                    if !loading && error == nil && model.connectionError == nil && current.isEmpty && model.decisionFeed.loaded && model.shownDecisions.isEmpty && model.leadReplacements.isEmpty {
                        ContentUnavailableView("Nothing needs you", systemImage: "tray", description: Text("Questions and approvals waiting on you, and instructions waiting to send, appear here. Everything else is in History."))
                            .listRowSeparator(.hidden)
                    }
                } else {
                    if history.isEmpty && !loading && !page.hasOlder {
                        ContentUnavailableView("No delivery history", systemImage: "clock", description: Text("Messages sent to your sessions appear here."))
                            .listRowSeparator(.hidden)
                    }
                    ForEach(history, id: \.listID) { message in
                        InboxHistoryRow(message: message, openSession: { open(message.sessionId) },
                                        answer: model.shownDecisions.decision(answering: message.askId).map { decision in { answering = decision } })
                    }
                    if page.hasOlder {
                        Button { Task { await loadOlder() } } label: {
                            HStack {
                                Text("Load older")
                                Spacer()
                                if loadingOlder { ProgressView() }
                            }.frame(minHeight: 44)
                        }
                        .disabled(loadingOlder)
                        .accessibilityIdentifier("inbox-load-older")
                    }
                }
            }
            .listRowBackground(GlassRowBackground(selected: false))
        }
        .listStyle(.insetGrouped)
        .phoneBackdrop(.pane)
        .navigationTitle("Inbox")
        .task { await reload() }
        .refreshable { _ = await model.refresh(fresh: true); await reload() }
        .onChange(of: activeInSnapshot) { _, _ in Task { await reload() } }
        .onChange(of: waitingInSnapshot) { _, _ in Task { await reload() } }
        .task(id: pollsHistory) {
            guard pollsHistory else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: InboxPage.historyPollInterval)
                if !Task.isCancelled { await reload() }
            }
        }
        .sheet(item: $answering) { item in
            DecisionSheet(model: model, item: item, title: model.sessionName(item.sessionId), open: { answering = nil; open(item.sessionId) }) {
                answering = nil
                Task { await reload() }
            }
        }
    }

    private func open(_ id: String) {
        model.openSession(id)
    }

    private func queueRow(_ item: QueueState) -> some View {
        let envelope = PeerEnvelope(item.message)
        let steering = model.queueSteer(item)
        let unconfirmed = item.status == "dead" && !item.isHistorical && item.evidence == "unknown"
        let review = model.shownDecisions.decision(answering: item.askId)
        return VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text(unconfirmed ? "Not confirmed" : item.outcomeLabel)
                    .font(.subheadline.weight(.semibold)).foregroundStyle(item.status == "dead" && !item.isHistorical ? PhonePalette.attention : PhonePalette.muted)
                Spacer()
                if let date = item.queuedDate {
                    Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.footnote).foregroundStyle(PhonePalette.muted)
                } else {
                    Text(item.queuedAt).font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1).truncationMode(.middle)
                }
            }
            if item.status == "pending" {
                Text(item.holdLabel).font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            InboxSessionLink(title: item.target, identifier: "inbox-session-\(item.id)") { open(item.sessionId) }
            if let sender = envelope.sender { Text("From \(sender)").font(.footnote).foregroundStyle(PhonePalette.muted) }
            Text(envelope.body).font(.body).lineLimit(4).textSelection(.enabled)
            if let attachments = item.attachments, !attachments.isEmpty {
                Label(attachments.map(\.name).joined(separator: ", "), systemImage: "paperclip")
                    .font(.footnote)
                    .foregroundStyle(PhonePalette.muted)
                    .lineLimit(2)
                    .accessibilityIdentifier("queue-attachments-\(item.listID)")
            }
            if unconfirmed {
                Text("Your message to \(item.target) may not have arrived. Open it to check.").font(.footnote).foregroundStyle(PhonePalette.muted)
            } else if let reason = item.lastError, !reason.isEmpty {
                Text(reason).font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            DisclosureGroup("Instruction details") {
                Text(envelope.body).textSelection(.enabled)
                LabeledContent("Attempts", value: item.attempts.formatted()).font(.footnote)
                if let value = item.model { LabeledContent("Model", value: value).font(.footnote) }
                if let value = item.serviceTier { LabeledContent("Service tier", value: value).font(.footnote) }
                if let value = item.sourceSessionId { LabeledContent("From session", value: value).font(.footnote).textSelection(.enabled) }
            }.font(.footnote)
            if let refusal = steering?.refusal {
                Text(refusal).font(.footnote).foregroundStyle(PhonePalette.muted)
                    .accessibilityIdentifier("queue-steer-refusal-\(item.id)")
            }
            HStack {
                if let decision = review {
                    Button("Answer") { answering = decision }.buttonStyle(.borderedProminent)
                        .accessibilityHint("Opens what to do with this message")
                        .accessibilityIdentifier("queue-answer-\(item.id)")
                    Button("Not now") {
                        Task {
                            if let askID = item.askId, let failure = await model.dismissDecision(model.shownDecisions.only(askID, of: decision)) { error = failure }
                            await reload()
                        }
                    }.buttonStyle(.bordered)
                        .accessibilityHint("Takes this off Needs you without answering")
                        .accessibilityIdentifier("queue-not-now-\(item.id)")
                }
                if let steering, steering.refusal == nil {
                    Button("Steer") { steer(item, capabilities: steering.capabilities) }.buttonStyle(.bordered)
                        .accessibilityHint("Sends this message into the running turn")
                        .accessibilityIdentifier("queue-steer-\(item.id)")
                }
                if item.status == "pending" && !item.isHistorical {
                    Button("Edit") { takeBack(item) }.buttonStyle(.bordered)
                        .accessibilityHint("Cancels this delivery and puts the message back in the composer")
                }
                if item.status == "pending" {
                    Button("Cancel delivery", role: .destructive) { cancel(item) }.buttonStyle(.bordered)
                }
            }
            .controlSize(.large)
            .disabled(model.pendingControls.contains("queue:\(item.listID)"))
        }.padding(.vertical, 6)
        .swipeActions(edge: .leading) {
            if item.status == "pending" && !item.isHistorical {
                Button("Edit") { takeBack(item) }.tint(.accentColor)
            }
        }
    }

    private func steer(_ item: QueueState, capabilities: Capabilities) {
        error = nil
        Task {
            do { try await model.steerQueued(item, capabilities: capabilities) } catch { self.error = error.localizedDescription }
            await reload()
        }
    }

    private func takeBack(_ item: QueueState) {
        Task {
            do {
                try await model.takeBack(item)
                error = nil
                open(item.sessionId)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func leadReplacementRow(_ replacement: LeadReplacement) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("@\(replacement.name) runs in a new conversation").font(.subheadline.weight(.semibold))
            Text(replacement.reason).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
            Text("Replaced by \(replacement.changedBy)").font(.footnote).foregroundStyle(PhonePalette.muted)
            Button("Acknowledge") {
                Task {
                    error = nil
                    do { try await model.acknowledgeLeadReplacement(replacement) }
                    catch { self.error = error.localizedDescription }
                }
            }
            .buttonStyle(.bordered)
            .disabled(model.pendingControls.contains("lead-replacement:\(replacement.listID)"))
        }
        .padding(.vertical, 6)
    }

    private func cancel(_ item: QueueState) {
        error = nil
        Task { do { try await model.cancelQueue(item); await reload() } catch { self.error = error.localizedDescription } }
    }
    private func reload() async {
        let requestID = UUID(); reloadID = requestID
        model.decisionFeed.reload()
        do {
            let first = try await model.inboxPage { partial in
                guard reloadID == requestID, !settled else { return }
                page = page.refreshed(with: partial); loading = false
            }
            guard reloadID == requestID, !Task.isCancelled else { return }
            page = page.refreshed(with: first); error = nil
        } catch {
            guard reloadID == requestID, !Task.isCancelled else { return }
            self.error = error.localizedDescription
        }
        loading = false
        settled = true
    }
    private func loadOlder() async {
        guard page.hasOlder, !loadingOlder else { return }
        loadingOlder = true
        defer { loadingOlder = false }
        do {
            let older = try await model.inboxPage(before: page.cursors)
            page = page.appending(older); error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}
