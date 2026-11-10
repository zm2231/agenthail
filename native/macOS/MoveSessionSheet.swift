import SwiftUI

struct MoveSessionSheet: View {
    var model: AgenthailModel
    let session: SessionState
    @Environment(\.dismiss) private var dismiss
    @State private var picked: String?

    private var hosts: [CreationHost] { model.moveTargets }
    private var target: CreationHost? { hosts.first { $0.id == picked && $0.isAvailable } }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text("Move \(session.displayName)")
                .font(.headline)
            Text("It stops here and continues on the computer you pick, in the same project. Messages sent during the move wait and follow it.")
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
                .fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                ForEach(hosts) { host in
                    Button { picked = host.id } label: {
                        HStack {
                            Image(systemName: picked == host.id ? "largecircle.fill.circle" : "circle")
                            Text(host.name)
                            Spacer()
                            if !host.isAvailable {
                                Text(host.stateLabel).foregroundStyle(DesktopPalette.text2)
                            }
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .disabled(!host.isAvailable)
                    .accessibilityIdentifier("move-target-\(host.name)")
                }
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Move") {
                    if let target { model.move(session, to: target) }
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(target == nil)
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 380)
    }
}

extension View {
    func moveNoticeAlert(_ model: AgenthailModel) -> some View {
        alert(model.moveNotice?.title ?? "", isPresented: Binding(get: { model.moveNotice != nil }, set: { if !$0 { model.moveNotice = nil } }), presenting: model.moveNotice) { notice in
            if notice.retry != nil {
                Button("Stop and Move", role: .destructive) { model.retryMove(notice) }
                Button("Cancel", role: .cancel) {}
            } else {
                Button("OK") {}
            }
        } message: { notice in
            Text(notice.message)
        }
    }
}
