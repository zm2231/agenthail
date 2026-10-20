import SwiftUI

struct MoveSessionSheet: View {
    @ObservedObject var model: AgenthailModel
    @StateObject private var controller: SessionMoveController
    @Environment(\.dismiss) private var dismiss

    init(model: AgenthailModel, request: SessionMoveRequest) {
        self.model = model
        _controller = StateObject(wrappedValue: SessionMoveController(session: request.session, adopt: request.adopt))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(controller.adopt ? "Open \(controller.session.title) in Agenthail" : "Move \(controller.session.title) to another account")
                .font(.headline)
            Text(controller.adopt ? "Agenthail stops the terminal process once it is idle and runs the conversation in its own host." : "The conversation keeps its history, name and queued messages, and spends the chosen account's usage from its next turn.")
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
                .fixedSize(horizontal: false, vertical: true)
            content
            HStack {
                Spacer()
                switch controller.phase {
                case .done:
                    Button("Done") { dismiss() }
                        .keyboardShortcut(.defaultAction)
                        .buttonStyle(.borderedProminent)
                case .confirmTerminal:
                    Button("Cancel") { controller.cancelConfirmation() }
                        .keyboardShortcut(.cancelAction)
                    Button("Stop Terminal and Move") { run(stopTerminal: true) }
                        .keyboardShortcut(.defaultAction)
                        .buttonStyle(.borderedProminent)
                default:
                    Button("Cancel") { dismiss() }
                        .keyboardShortcut(.cancelAction)
                    Button(controller.phase == .moving ? "Moving…" : (controller.adopt ? "Open" : "Move")) { run(stopTerminal: false) }
                        .keyboardShortcut(.defaultAction)
                        .buttonStyle(.borderedProminent)
                        .disabled(!controller.canMove)
                }
            }
        }
        .padding(20)
        .frame(width: 460)
        .task { if let api = model.api { await controller.load(api: api) } }
    }

    @ViewBuilder private var content: some View {
        switch controller.phase {
        case .loading:
            ProgressView().controlSize(.small)
        case .confirmTerminal(let message):
            Text(message + " Stopping it ends the terminal session; the conversation continues in Agenthail.")
                .font(.system(size: 12))
                .fixedSize(horizontal: false, vertical: true)
        case .done(let summary):
            Text(summary).font(.system(size: 12))
        case .failed(let message):
            Text(message)
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.red)
                .fixedSize(horizontal: false, vertical: true)
            if !controller.adopt && !controller.accounts.isEmpty { picker }
        case .choosing, .moving:
            if !controller.adopt {
                picker
                if controller.sharesAccount {
                    Text("Both directories are signed into the same account, so the session still spends that account's usage.")
                        .font(.system(size: 12))
                        .foregroundStyle(DesktopPalette.text2)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }

    private var picker: some View {
        Picker("Account", selection: $controller.selected) {
            ForEach(controller.accounts) { account in
                Text(account.root == controller.currentRoot ? "\(account.menuLabel) · current" : account.menuLabel)
                    .tag(account.root)
                    .selectionDisabled(!account.signedIn || account.root == controller.currentRoot)
                    .help(account.root)
            }
        }
        .disabled(controller.phase == .moving)
    }

    private func run(stopTerminal: Bool) {
        guard let api = model.api else { return }
        Task {
            await controller.move(api: api, stopTerminal: stopTerminal)
            if case .done = controller.phase { _ = await model.refresh(fresh: true) }
        }
    }
}
