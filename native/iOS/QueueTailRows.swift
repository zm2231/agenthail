import SwiftUI

struct QueueTailRows: View {
    @ObservedObject var model: AgenthailIOSModel
    @Binding var tail: QueueTail
    let readOnly: Bool
    var generation = 0
    @Environment(\.verticalSizeClass) private var verticalSizeClass
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var retrying: QueueTail.Row?
    @State private var reading: QueueTail.Row?

    var body: some View {
        if tail.hiddenCount > 0 {
            Button { tail.expanded = true } label: {
                HStack(spacing: 6) {
                    Image(systemName: "chevron.right").font(.caption2.weight(.semibold))
                    Text("\(tail.hiddenCount) more queued").font(.footnote.weight(.semibold))
                }
                .foregroundStyle(PhonePalette.muted)
                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("\(tail.hiddenCount) more queued")
            .accessibilityHint("Shows every queued message")
            .accessibilityIdentifier("queue-tail-more")
        }
        if let problem = tail.problem {
            Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed)
                .accessibilityIdentifier("queue-tail-problem")
        }
        ForEach(tail.shown) { row in
            bubble(row)
                .rebuilt(row.id, generation: generation)
                .id(row.id)
                .accessibilityIdentifier("queue-\(row.item.listID)")
                .confirmationDialog("Send this message again?", isPresented: Binding(get: { retrying?.id == row.id }, set: { if !$0 { retrying = nil } }), titleVisibility: .visible) {
                    Button("Send again") { run(row, leaves: false) { try await model.updateQueue(row.item, retry: true) } }
                    Button("Cancel", role: .cancel) { retrying = nil }
                } message: {
                    Text("It may already have reached the agent. Check the session first.")
                }
                .sheet(isPresented: Binding(get: { reading?.id == row.id }, set: { if !$0 { reading = nil } })) {
                    QueuedMessageSheet(item: row.item, reason: row.reason)
                }
        }
    }

    private func bubble(_ row: QueueTail.Row) -> some View {
        let item = row.item
        let names = (item.attachments ?? []).map(\.name).joined(separator: ", ")
        let compact = verticalSizeClass == .compact && !dynamicTypeSize.isAccessibilitySize
        let heading = Text(row.heading)
            .font(.footnote.weight(.semibold))
            .foregroundStyle(row.isDead ? AnyShapeStyle(PhonePalette.attention) : AnyShapeStyle(PhonePalette.muted))
        return VStack(alignment: .leading, spacing: 4) {
            if !compact { heading }
            HStack(spacing: 6) {
                if compact { heading.lineLimit(1).fixedSize() }
                Button { reading = row } label: {
                    VStack(alignment: .leading, spacing: 1) {
                        Text(PeerEnvelope(item.message).body.split(separator: "\n").first.map(String.init) ?? "")
                            .font(.subheadline).lineLimit(1).truncationMode(.tail)
                        if !names.isEmpty {
                            Label(names, systemImage: "paperclip").font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1)
                        }
                    }
                    .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityHint("Shows the whole message")
                .accessibilityIdentifier("queue-tail-read-\(item.id)")
                actions(row)
            }
            if !compact, let reason = row.reason {
                Text(reason)
                    .font(.footnote).foregroundStyle(PhonePalette.muted)
                    .accessibilityIdentifier("queue-tail-reason-\(item.id)")
            }
        }
        .padding(.horizontal, 14).padding(.vertical, compact ? 4 : 8)
        .background(SessionStyle.surface, in: RoundedRectangle(cornerRadius: 16))
        .padding(.leading, 16)
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder private func actions(_ row: QueueTail.Row) -> some View {
        let item = row.item
        let idle = !model.pendingControls.contains("queue:\(item.listID)")
        switch row.controls {
        case .review:
            control("Send again", id: "queue-tail-retry-\(item.id)", enabled: idle) { retrying = row }
            control("Dismiss", id: "queue-tail-dismiss-\(item.id)", enabled: idle) {
                run(row, leaves: true) { try await model.updateQueue(item, retry: false) }
            }
        case .waiting:
            let steering = model.queueSteer(item)
            if let steering, steering.refusal == nil {
                control("Steer", id: "queue-tail-steer-\(item.id)", enabled: idle) {
                    run(row, leaves: false) {
                        let steered = try await model.steerQueued(item, capabilities: steering.capabilities)
                        if steered { tail.steered(row, now: Date()) }
                        return steered
                    }
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
        case .none:
            EmptyView()
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

    private func run(_ row: QueueTail.Row, leaves: Bool, _ perform: @escaping () async throws -> Bool) {
        retrying = nil
        let generation = tail.generation
        tail.report(nil, from: generation)
        Task {
            do {
                let done = try await perform()
                if done, leaves, tail.generation == generation { tail.drop(row) }
            } catch {
                tail.report(AgenthailAPIError.userMessage(error), from: generation)
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

    private var turnRunning: Bool {
        (model.snapshot?.sessions.first { $0.id == session.id } ?? session).status == "busy"
    }

    private func settle() { tail.update(active: active, timeline: timeline, turnRunning: turnRunning, now: Date()) }

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
            .onChange(of: turnRunning) { _, _ in settle() }
    }
}
