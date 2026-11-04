import SwiftUI

struct QueueTailRows: View {
    @ObservedObject var model: AgenthailIOSModel
    @Binding var tail: QueueTail
    let readOnly: Bool
    @State private var problem: String?
    @State private var retrying: QueueTail.Row?

    var body: some View {
        if tail.hiddenCount > 0 {
            Button { tail.expanded = true } label: {
                Text("\(tail.hiddenCount) more queued")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(PhonePalette.muted)
                    .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("queue-tail-more")
        }
        if let problem {
            Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed)
                .accessibilityIdentifier("queue-tail-problem")
        }
        ForEach(tail.shown) { row in
            bubble(row)
                .id(row.id)
                .accessibilityIdentifier(row.id)
                .confirmationDialog("Send this message again?", isPresented: Binding(get: { retrying?.id == row.id }, set: { if !$0 { retrying = nil } }), titleVisibility: .visible) {
                    Button("Send again") { run(row, leaves: false) { try await model.updateQueue(row.item, retry: true) } }
                    Button("Cancel", role: .cancel) { retrying = nil }
                } message: {
                    Text("It may already have reached the agent. Check the session first.")
                }
        }
    }

    private func label(_ row: QueueTail.Row) -> String {
        if row.leavingSince != nil { return "Delivering" }
        if row.isDead { return "Delivery needs review" }
        if let sender = PeerEnvelope(row.item.message).sender { return "Queued · From \(sender)" }
        return row.item.status == "inflight" ? "Delivering" : "Queued"
    }

    private func bubble(_ row: QueueTail.Row) -> some View {
        let item = row.item
        let names = (item.attachments ?? []).map(\.name).joined(separator: ", ")
        return VStack(alignment: .leading, spacing: 4) {
            Text(label(row))
                .font(.footnote.weight(.semibold))
                .foregroundStyle(row.isDead ? AnyShapeStyle(PhonePalette.attention) : AnyShapeStyle(PhonePalette.muted))
            HStack(spacing: 6) {
                VStack(alignment: .leading, spacing: 1) {
                    Text(PeerEnvelope(item.message).body.split(separator: "\n").first.map(String.init) ?? "")
                        .font(.subheadline).lineLimit(1).truncationMode(.tail)
                    if !names.isEmpty {
                        Label(names, systemImage: "paperclip").font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                if row.leavingSince == nil { actions(row) }
            }
            if row.isDead {
                Text(item.lastError.flatMap { $0.isEmpty ? nil : $0 } ?? "It may already have reached the agent. Check the session before sending again.")
                    .font(.footnote).foregroundStyle(PhonePalette.muted)
                    .accessibilityIdentifier("queue-tail-reason-\(item.id)")
            }
        }
        .padding(.horizontal, 14).padding(.vertical, 8)
        .background(SessionStyle.surface, in: RoundedRectangle(cornerRadius: 16))
        .padding(.leading, 16)
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder private func actions(_ row: QueueTail.Row) -> some View {
        let item = row.item
        let idle = !model.pendingControls.contains("queue:\(item.listID)")
        if row.isDead {
            control("Send again", id: "queue-tail-retry-\(item.id)", enabled: idle) { retrying = row }
            control("Dismiss", id: "queue-tail-dismiss-\(item.id)", enabled: idle) {
                run(row, leaves: true) { try await model.updateQueue(item, retry: false) }
            }
        } else if item.status == "pending" {
            let steering = model.queueSteer(item)
            if let steering, steering.refusal == nil {
                control("Steer", id: "queue-tail-steer-\(item.id)", enabled: idle) {
                    run(row, leaves: true) { try await model.steerQueued(item, capabilities: steering.capabilities) }
                }
            }
            Menu {
                if let refusal = steering?.refusal {
                    Section(refusal) { Button("Steer") {}.disabled(true) }
                }
                if !readOnly {
                    Button("Edit", systemImage: "pencil") { run(row, leaves: true) { try await model.takeBack(item) } }
                }
                Button("Cancel delivery", systemImage: "xmark", role: .destructive) {
                    run(row, leaves: true) { try await model.updateQueue(item, retry: false) }
                }
            } label: {
                Image(systemName: "ellipsis")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(PhonePalette.muted)
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .disabled(!idle)
            .accessibilityLabel("More actions for this queued message")
            .accessibilityIdentifier("queue-tail-menu-\(item.id)")
        }
    }

    private func control(_ title: String, id: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(title, action: action)
            .font(.footnote.weight(.semibold))
            .frame(minWidth: 44, minHeight: 44)
            .contentShape(Rectangle())
            .buttonStyle(.plain)
            .foregroundStyle(enabled ? AnyShapeStyle(PhonePalette.accent) : AnyShapeStyle(PhonePalette.muted))
            .disabled(!enabled)
            .accessibilityIdentifier(id)
    }

    private func run(_ row: QueueTail.Row, leaves: Bool, _ perform: @escaping () async throws -> Void) {
        retrying = nil
        problem = nil
        Task {
            do {
                try await perform()
                if leaves { tail.drop(row) }
            } catch {
                problem = AgenthailAPIError.userMessage(error)
            }
        }
    }
}

struct QueueTailTracking: ViewModifier {
    @ObservedObject var model: AgenthailIOSModel
    @Binding var tail: QueueTail
    let session: SessionState
    let timeline: [TimelineItem]
    @State private var loaded: [QueueState]?

    private var snapshotRows: [QueueState] {
        (model.snapshot?.queue ?? []).map { InboxPage.qualified($0, host: $0.hostID ?? "") }.filter { $0.sessionId == session.id }
    }
    private var active: [QueueState] { loaded ?? snapshotRows }
    private var refreshKey: String {
        let listed = snapshotRows.map { "\($0.id):\($0.status)" }
        return ([session.id, session.status, String(session.queueCount), model.deliveryStatus[session.id] ?? ""] + listed).joined(separator: "|")
    }

    private func settle() { tail.update(active: active, timeline: timeline, now: Date()) }

    func body(content: Content) -> some View {
        content
            .task(id: session.id) {
                loaded = nil
                tail = QueueTail()
                settle()
            }
            .task(id: refreshKey) {
                let fresh = try? await model.sessionQueue(session.id)
                guard !Task.isCancelled else { return }
                loaded = fresh
                settle()
            }
            .task(id: tail.nextDeparture) {
                guard let at = tail.nextDeparture else { return }
                try? await Task.sleep(for: .seconds(max(at.timeIntervalSinceNow, 0) + 0.05))
                guard !Task.isCancelled else { return }
                settle()
            }
            .onChange(of: timeline) { _, _ in settle() }
    }
}
