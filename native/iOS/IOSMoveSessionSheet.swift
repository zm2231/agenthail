import SwiftUI

struct IOSMoveSessionSheet: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let moved: () -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(model.moveTargets(session)) { host in
                        Button {
                            model.move(session, to: host)
                            dismiss()
                            moved()
                        } label: {
                            LabeledContent(host.name, value: host.isAvailable ? "" : host.stateLabel)
                        }
                        .disabled(!host.isAvailable)
                        .accessibilityIdentifier("move-target-\(host.name)")
                    }
                } footer: {
                    Text("It stops here and continues on the computer you pick, in the same project. Messages sent during the move wait and follow it.")
                }
            }
            .navigationTitle("Move \(session.displayName)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
        }
    }
}

extension View {
    func moveNoticeAlert(_ model: AgenthailIOSModel) -> some View {
        let notice = model.moveNotices.current
        return alert(notice?.title ?? "", isPresented: Binding(get: { notice != nil }, set: { _ in }), presenting: notice) { notice in
            if notice.retry != nil {
                Button("Stop and Move", role: .destructive) {
                    model.moveNotices.dismiss(notice)
                    model.retryMove(notice)
                }
                Button("Cancel", role: .cancel) { model.moveNotices.dismiss(notice) }
            } else {
                Button("OK") { model.moveNotices.dismiss(notice) }
            }
        } message: { notice in
            Text(notice.message)
        }
    }
}
