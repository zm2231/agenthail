import SwiftUI

struct IOSNameSessionSheet: View {
    var model: AgenthailIOSModel
    let sessionID: String
    let alias: String?
    let close: () -> Void
    @State private var text: String
    @State private var error: String?

    init(model: AgenthailIOSModel, sessionID: String, alias: String?, close: @escaping () -> Void) {
        self.model = model
        self.sessionID = sessionID
        self.alias = alias
        self.close = close
        _text = State(initialValue: alias ?? "")
    }

    private var pending: Bool { model.pendingControls.contains(sessionID) }
    private var problem: String? { SessionHandle.renameProblem(text, current: alias) }
    private var clearing: Bool { SessionHandle.clears(text, current: alias) }
    private var canSave: Bool { problem == nil && SessionHandle.normalized(text) != (alias ?? "") }

    var body: some View {
        NavigationStack {
            Form {
                TextField("Name", text: $text, axis: .vertical).lineLimit(2...8)
                    .textInputAutocapitalization(.never).autocorrectionDisabled(true)
                Text((alias ?? "").isEmpty ? "Use 1–80 characters without spaces, /, or #." : "Use 1–80 characters without spaces, /, or #. Leave it empty to clear the name.").font(.footnote)
                if let message = error ?? (!text.isEmpty ? problem : nil) { Text(message).foregroundStyle(PhonePalette.failed) }
            }
            .navigationTitle("Name conversation")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { close() }.disabled(pending) }
                ToolbarItem(placement: .confirmationAction) {
                    Button(clearing ? "Clear Name" : "Save") { Task { await save() } }
                        .disabled(!canSave || pending)
                }
            }
            .interactiveDismissDisabled(pending)
        }
    }

    private func save() async {
        error = nil
        do {
            try await model.editSession(id: sessionID, action: "alias", text: SessionHandle.normalized(text))
            close()
        } catch is LeadNameRefused {
            error = SessionHandle.leadRefusal(alias)
        } catch {
            self.error = error.localizedDescription
        }
    }
}
