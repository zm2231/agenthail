import SwiftUI

struct SessionQueueSection: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    let capabilities: Capabilities
    let readOnly: Bool
    @State private var items: [QueueState] = []
    @State private var problem: String?
    @State private var expanded = true

    private var canSteer: Bool { session.status == "busy" && session.waitingOn == nil && capabilities.steer && !readOnly }

    private var refreshKey: String {
        let listed = (model.snapshot?.queue ?? []).filter { $0.sessionId == session.id }.map { "\($0.id):\($0.status)" }
        return ([session.status, String(session.queueCount), model.deliveryStatus[session.id] ?? ""] + listed).joined(separator: "|")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if !items.isEmpty {
                Button { expanded.toggle() } label: {
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
                if expanded {
                    ViewThatFits(in: .vertical) {
                        rows
                        ScrollView { rows }
                    }
                    .frame(maxHeight: 200)
                }
            }
            if let problem {
                Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed)
                    .accessibilityIdentifier("session-queue-problem")
            }
        }
        .padding(.horizontal, 8)
        .task(id: refreshKey) { await reload() }
    }

    private var rows: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(items, id: \.listID) { item in row(item) }
        }
    }

    private func row(_ item: QueueState) -> some View {
        let refusal = canSteer ? QueueSteer.refusal(item, capabilities: capabilities) : nil
        return VStack(alignment: .leading, spacing: 2) {
            if let line = item.message.split(separator: "\n").first {
                Text(line).font(.subheadline).lineLimit(1).truncationMode(.tail)
            }
            if let attachments = item.attachments, !attachments.isEmpty {
                Label(attachments.map(\.name).joined(separator: ", "), systemImage: "paperclip")
                    .font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1).truncationMode(.middle)
                    .accessibilityIdentifier("session-queue-attachments-\(item.id)")
            }
            Text(item.holdLabel).font(.footnote).foregroundStyle(PhonePalette.muted)
            if let refusal {
                Text(refusal).font(.footnote).foregroundStyle(PhonePalette.muted)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("session-queue-refusal-\(item.id)")
            }
            HStack(spacing: 18) {
                let idle = !model.pendingControls.contains("queue:\(item.listID)")
                if canSteer {
                    action("Steer", "session-queue-steer-\(item.id)", enabled: idle && refusal == nil) { try await model.steerQueued(item, capabilities: capabilities) }
                }
                action("Edit", "session-queue-edit-\(item.id)", enabled: idle) { try await model.takeBack(item) }
                action("Cancel", "session-queue-cancel-\(item.id)", enabled: idle) { try await model.updateQueue(item, retry: false) }
            }
        }
        .accessibilityElement(children: .contain)
    }

    private func action(_ title: String, _ identifier: String, enabled: Bool, _ perform: @escaping () async throws -> Void) -> some View {
        Button(title) {
            Task {
                problem = nil
                do { try await perform() } catch { problem = AgenthailAPIError.userMessage(error) }
                await reload()
            }
        }
        .font(.footnote.weight(.semibold))
        .frame(minHeight: 44)
        .contentShape(Rectangle())
        .buttonStyle(.plain)
        .foregroundStyle(enabled ? AnyShapeStyle(PhonePalette.accent) : AnyShapeStyle(PhonePalette.muted))
        .disabled(!enabled)
        .accessibilityIdentifier(identifier)
    }

    private func reload() async {
        guard let loaded = try? await model.sessionQueue(session.id), !Task.isCancelled else { return }
        items = loaded
    }
}
