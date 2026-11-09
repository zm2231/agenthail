import SwiftUI

struct IOSProjectMessageSheet: View {
    let project: ProjectState
    let send: (String, String) async throws -> String
    @Environment(\.dismiss) private var dismiss
    @State private var text = ""
    @State private var key = UUID().uuidString
    @State private var sending = false
    @State private var problem: String?
    @State private var sent: String?

    init(project: ProjectState, send: @escaping (String, String) async throws -> String) {
        self.project = project
        self.send = send
    }

    init(model: AgenthailIOSModel, project: ListedProject) {
        self.init(project: project.state) { message, key in
            try await model.broadcast(to: project, message: message, idempotencyKey: key)
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                if let sent {
                    Text(sent).textSelection(.enabled).accessibilityIdentifier("project-message-sent")
                } else {
                    Section {
                        TextField("Message", text: $text, axis: .vertical)
                            .lineLimit(4...10)
                            .disabled(sending)
                            .accessibilityIdentifier("project-message-text")
                    } footer: {
                        Text(ProjectBroadcast.prompt(project))
                    }
                    if let problem { Text(problem).foregroundStyle(PhonePalette.failed) }
                }
            }
            .navigationTitle("Message \(project.displayName)")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                if sent != nil {
                    ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
                } else {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.disabled(sending) }
                    ToolbarItem(placement: .confirmationAction) {
                        Button(sending ? "Sending…" : "Send") { Task { await submit() } }
                            .disabled(sending || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                            .accessibilityIdentifier("project-message-send")
                    }
                }
            }
            .interactiveDismissDisabled(sending)
            .onChange(of: text) { key = UUID().uuidString }
        }
    }

    private func submit() async {
        sending = true
        problem = nil
        defer { sending = false }
        do {
            sent = try await send(text, key)
        } catch {
            problem = AgenthailAPIError.userMessage(error)
        }
    }
}

extension ListedProject: Identifiable {
    var id: String { ProjectContents.key(of: self) }
}
