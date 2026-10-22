import SwiftUI

struct AdoptSessionSheet: View {
    @ObservedObject var model: AgenthailModel
    @StateObject private var controller: SessionAdoptController
    @Environment(\.dismiss) private var dismiss

    init(model: AgenthailModel, session: SessionState) {
        self.model = model
        _controller = StateObject(wrappedValue: SessionAdoptController(session: session))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text("Open \(controller.session.title) in Agenthail")
                .font(.headline)
            Text("Agenthail stops the process that runs the conversation once it is idle and continues it in its own host, in the same config folder.")
                .font(.system(size: DesktopType.secondary))
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
                    Button("Stop Terminal and Open") { run(stopTerminal: true) }
                        .keyboardShortcut(.defaultAction)
                        .buttonStyle(.borderedProminent)
                default:
                    Button("Cancel") { dismiss() }
                        .keyboardShortcut(.cancelAction)
                    Button(controller.phase == .adopting ? "Opening…" : "Open") { run(stopTerminal: false) }
                        .keyboardShortcut(.defaultAction)
                        .buttonStyle(.borderedProminent)
                        .disabled(!controller.canAdopt)
                }
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 460)
    }

    @ViewBuilder private var content: some View {
        switch controller.phase {
        case .ready, .adopting:
            EmptyView()
        case .confirmTerminal(let message):
            Text(message + " Stopping it ends the terminal session; the conversation continues in Agenthail.")
                .font(.system(size: DesktopType.secondary))
                .fixedSize(horizontal: false, vertical: true)
        case .done(let summary):
            Text(summary).font(.system(size: DesktopType.secondary))
        case .failed(let message):
            Text(message)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.failed)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func run(stopTerminal: Bool) {
        guard let api = model.api else { return }
        Task {
            await controller.adopt(api: api, stopTerminal: stopTerminal)
            if case .done = controller.phase { _ = await model.refresh(fresh: true) }
        }
    }
}
