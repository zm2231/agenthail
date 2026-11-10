import SwiftUI

struct SessionOperationTarget {
    let hostID: String?
    let sessionID: String
    let api: AgenthailAPI?
    let keys: ActionRetryKeys
    let unavailable: String?

    var identity: String { [hostID ?? "", sessionID].joined(separator: "/") }
}

struct IOSSessionOperations: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let readOnlyReason: String

    var body: some View {
        let target = model.operationTarget(session.id)
        if SessionOperationAvailability.nativeQueue(session) {
            IOSQueueSection(model: model, session: session, target: target, readOnlyReason: readOnlyReason)
                .id(target.identity)
        }
        let lifecycle = SessionOperationAvailability.lifecycle(session)
        if lifecycle.any {
            IOSLifecycleSection(model: model, session: session, target: target, lifecycle: lifecycle)
                .id(target.identity)
        }
    }
}

struct IOSForkSessionButton: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let dismiss: DismissAction
    @State private var forking = false
    @State private var error: String?

    var body: some View {
        if SessionOperationAvailability.fork(session) {
            let unavailable = model.operationTarget(session.id).unavailable
            Button(forking ? "Forking…" : "Fork session", systemImage: "arrow.triangle.branch") { fork() }
                .disabled(forking || unavailable != nil)
                .accessibilityIdentifier("session-fork")
            if let message = error ?? unavailable {
                Text(message).font(.footnote).foregroundStyle(error == nil ? PhonePalette.muted : PhonePalette.failed)
            }
        }
    }

    private func fork() {
        forking = true
        error = nil
        Task {
            do {
                try await model.fork(session)
                forking = false
                dismiss()
            } catch {
                forking = false
                self.error = AgenthailAPIError.userMessage(error)
            }
        }
    }
}
