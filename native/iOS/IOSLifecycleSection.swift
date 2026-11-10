import SwiftUI
import UIKit

struct IOSLifecycleSection: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let target: SessionOperationTarget
    let lifecycle: SessionLifecycle
    @StateObject private var controller: BackgroundSessionController

    init(model: AgenthailIOSModel, session: SessionState, target: SessionOperationTarget, lifecycle: SessionLifecycle) {
        self.model = model
        self.session = session
        self.target = target
        self.lifecycle = lifecycle
        _controller = StateObject(wrappedValue: BackgroundSessionController(keys: target.keys))
    }

    var body: some View {
        Section {
            if let status = controller.status {
                Text([status.state.map { "State: \($0)" }, status.id.map { "Job \($0)" }].compactMap { $0 }.joined(separator: " · "))
                    .textSelection(.enabled)
                if controller.resumeUnchanged {
                    Text("Already running, nothing to resume.").font(.footnote).foregroundStyle(PhonePalette.muted)
                }
            } else {
                Text(SessionLifecycleCopy.detail(session)).font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            Group {
                if lifecycle.status { Button("Status", systemImage: "info.circle") { run("status") }.accessibilityIdentifier("lifecycle-status") }
                if lifecycle.logs { Button("Logs", systemImage: "doc.text") { run("logs") }.accessibilityIdentifier("lifecycle-logs") }
                if lifecycle.stop { Button("Stop", systemImage: "stop.circle", role: .destructive) { run("stop") }.accessibilityIdentifier("lifecycle-stop") }
                if lifecycle.resume { Button("Resume", systemImage: "play.circle") { run("resume") }.accessibilityIdentifier("lifecycle-resume") }
            }
            .disabled(controller.busyAction != nil || target.api == nil)
            if let error = controller.error {
                Text(error).font(.footnote).foregroundStyle(PhonePalette.failed).textSelection(.enabled)
            }
        } header: {
            HStack {
                Text(SessionLifecycleCopy.title(session))
                Spacer()
                if controller.busyAction != nil { ProgressView() }
            }
        } footer: {
            if let unavailable = target.unavailable { Text(unavailable) }
        }
        .task(id: target.identity) { controller.reset(sessionID: target.sessionID) }
        .sheet(isPresented: Binding(get: { controller.logs != nil }, set: { if !$0 { controller.logs = nil } })) {
            IOSLogsSheet(title: session.title, logs: controller.logs ?? "")
        }
    }

    private func run(_ action: String) {
        Task {
            if await controller.run(action, api: target.api), action == "stop" || action == "resume" {
                await model.refreshSession(session.id)
            }
        }
    }
}

struct IOSLogsSheet: View {
    let title: String
    let logs: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                Text(logs.isEmpty ? "No log output." : logs)
                    .font(.footnote.monospaced())
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding()
            }
            .navigationTitle("Logs for \(title)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Copy", systemImage: "doc.on.doc") { UIPasteboard.general.string = logs }
                }
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }
}
