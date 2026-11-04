import SwiftUI

struct SessionQueueSection: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    let capabilities: Capabilities
    let readOnly: Bool
    @State private var loaded: [QueueState]?
    @State private var problem: String?
    @State private var chosenExpanded: Bool?
    @State private var rowsHeight: CGFloat = 0

    private static let collapsesAt = 3
    private static let heightCap: CGFloat = 156

    // Rows known from the snapshot render on the first frame, so the card doesn't appear while the transcript lands.
    private var snapshotRows: [QueueState] {
        (model.snapshot?.queue ?? []).map { InboxPage.qualified($0, host: $0.hostID ?? "") }.filter { $0.sessionId == session.id }
    }
    private var items: [QueueState] { loaded ?? QueueSteer.waiting(snapshotRows) }
    private var expanded: Bool { chosenExpanded ?? (items.count < Self.collapsesAt) }
    private var canSteer: Bool { session.status == "busy" && session.waitingOn == nil && capabilities.steer && !readOnly }

    private var refreshKey: String {
        let listed = snapshotRows.map { "\($0.id):\($0.status)" }
        return ([session.status, String(session.queueCount), model.deliveryStatus[session.id] ?? ""] + listed).joined(separator: "|")
    }

    var body: some View {
        VStack(spacing: 0) {
            if !items.isEmpty || problem != nil {
                card
                    .padding(.horizontal, 18)
                    .padding(.vertical, 8)
                    .floatingGlass(in: RoundedRectangle(cornerRadius: 26, style: .continuous))
            }
        }
        .task(id: refreshKey) { await reload() }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 0) {
            if !items.isEmpty {
                Button { chosenExpanded = !expanded } label: {
                    HStack(spacing: 6) {
                        Image(systemName: expanded ? "chevron.down" : "chevron.right").font(.caption2.weight(.semibold))
                        Text("Queued · \(items.count)").font(.footnote.weight(.semibold))
                        Spacer(minLength: 0)
                    }
                    .foregroundStyle(PhonePalette.muted)
                    .frame(minHeight: 44)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("session-queue-header")
                if expanded { scrollingRows }
            }
            if let problem {
                Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed)
                    .accessibilityIdentifier("session-queue-problem")
            }
        }
    }

    private var rows: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(items, id: \.listID) { item in row(item) }
        }
    }

    private var scrollingRows: some View {
        let overflows = rowsHeight > Self.heightCap
        return ScrollView {
            rows
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { rowsHeight = $0 }
                .padding(.bottom, overflows ? 22 : 0)
        }
        .frame(height: min(rowsHeight, Self.heightCap))
        .scrollBounceBehavior(.basedOnSize)
        .scrollIndicators(.visible)
        .scrollIndicatorsFlash(trigger: overflows)
        .mask {
            LinearGradient(stops: [.init(color: .black, location: 0), .init(color: .black, location: overflows ? 0.82 : 1), .init(color: overflows ? .clear : .black, location: 1)], startPoint: .top, endPoint: .bottom)
        }
        .accessibilityIdentifier("session-queue-scroll")
    }

    private func row(_ item: QueueState) -> some View {
        let refusal = canSteer ? QueueSteer.refusal(item, capabilities: capabilities) : nil
        let idle = !model.pendingControls.contains("queue:\(item.listID)")
        return HStack(spacing: 6) {
            VStack(alignment: .leading, spacing: 1) {
                if let line = PeerEnvelope(item.message).summary.split(separator: "\n").first {
                    Text(line).font(.subheadline).lineLimit(1).truncationMode(.tail)
                }
                details(item, refused: refusal != nil)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if canSteer && refusal == nil {
                Button("Steer") { run { try await model.steerQueued(item, capabilities: capabilities) } }
                    .font(.footnote.weight(.semibold))
                    .frame(minWidth: 44, minHeight: 44)
                    .contentShape(Rectangle())
                    .buttonStyle(.plain)
                    .foregroundStyle(idle ? AnyShapeStyle(PhonePalette.accent) : AnyShapeStyle(PhonePalette.muted))
                    .disabled(!idle)
                    .accessibilityIdentifier("session-queue-steer-\(item.id)")
            }
            Menu {
                if let refusal {
                    Section(refusal) { Button("Steer") {}.disabled(true) }
                }
                Button("Edit", systemImage: "pencil") { run { try await model.takeBack(item) } }
                Button("Cancel delivery", systemImage: "xmark", role: .destructive) { run { try await model.updateQueue(item, retry: false) } }
            } label: {
                Image(systemName: "ellipsis")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(PhonePalette.muted)
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .disabled(!idle)
            .accessibilityLabel("More actions for this queued message")
            .accessibilityIdentifier("session-queue-menu-\(item.id)")
        }
        .accessibilityElement(children: .contain)
    }

    @ViewBuilder private func details(_ item: QueueState, refused: Bool) -> some View {
        let names = (item.attachments ?? []).map(\.name).joined(separator: ", ")
        let reason = refused ? "Can't steer" : news(item)
        if !names.isEmpty || reason != nil {
            HStack(spacing: 4) {
                if !names.isEmpty {
                    Image(systemName: "paperclip").imageScale(.small)
                    Text(names).lineLimit(1).truncationMode(.middle)
                        .accessibilityIdentifier("session-queue-attachments-\(item.id)")
                }
                if let reason {
                    Text(names.isEmpty ? reason : "· \(reason)").lineLimit(1).layoutPriority(1)
                        .accessibilityIdentifier("session-queue-reason-\(item.id)")
                }
            }
            .font(.footnote)
            .foregroundStyle(PhonePalette.muted)
        }
    }

    private func news(_ item: QueueState) -> String? {
        switch item.heldBy {
        case nil, "busy": return nil
        default: return item.holdLabel
        }
    }

    private func run(_ perform: @escaping () async throws -> Void) {
        Task {
            problem = nil
            do { try await perform() } catch { problem = AgenthailAPIError.userMessage(error) }
            await reload()
        }
    }

    private func reload() async {
        let fresh = try? await model.sessionQueue(session.id)
        guard !Task.isCancelled else { return }
        loaded = fresh
    }
}
