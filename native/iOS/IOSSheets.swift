import SwiftUI

struct IOSAdoptSessionSheet: View {
    var model: AgenthailIOSModel
    @StateObject private var controller: SessionAdoptController
    @Environment(\.dismiss) private var dismiss

    private let sessionID: String

    init(model: AgenthailIOSModel, session: SessionState, sessionID: String) {
        self.model = model
        self.sessionID = sessionID
        _controller = StateObject(wrappedValue: SessionAdoptController(session: session))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text("Agenthail stops the process that runs the conversation once it is idle and continues it in its own host, in the same config folder.")
                        .font(.footnote)
                        .foregroundStyle(PhonePalette.muted)
                }
                switch controller.phase {
                case .ready, .adopting:
                    EmptyView()
                case .confirmTerminal(let message):
                    Section {
                        Text(message + " Stopping it ends the terminal session; the conversation continues in Agenthail.")
                        Button("Stop Terminal and Open", role: .destructive) { run(stopTerminal: true) }
                        Button("Cancel") { controller.cancelConfirmation() }
                    }
                case .done(let summary):
                    Section { Text(summary) }
                case .failed(let message):
                    Section { Text(message).foregroundStyle(PhonePalette.failed) }
                }
            }
            .navigationTitle("Open in Agenthail")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    if case .done = controller.phase {} else { Button("Cancel") { dismiss() } }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if case .done = controller.phase {
                        Button("Done") { dismiss() }
                    } else {
                        Button(controller.phase == .adopting ? "Opening…" : "Open") { run(stopTerminal: false) }
                            .disabled(!controller.canAdopt)
                    }
                }
            }
        }
    }

    private func run(stopTerminal: Bool) {
        guard let api = model.routedAPI(for: sessionID) else { return }
        Task {
            await controller.adopt(api: api, stopTerminal: stopTerminal)
            if case .done = controller.phase { _ = await model.refresh(fresh: true) }
        }
    }
}

struct RecentPromptsSheet: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let use: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @State private var allSessions = false
    @State private var prompts: [PromptItem] = []
    @State private var problems: [String] = []
    @State private var loading = true

    var body: some View {
        NavigationStack {
            List {
                Picker("Show", selection: $allSessions) {
                    Text("This session").tag(false)
                    Text("All sessions").tag(true)
                }
                .pickerStyle(.segmented)
                ForEach(problems, id: \.self) { Text($0).font(.footnote).foregroundStyle(PhonePalette.failed) }
                if prompts.isEmpty && !loading {
                    Text(query.isEmpty ? "Nothing sent yet." : "No sent prompt matches.").foregroundStyle(PhonePalette.muted)
                }
                ForEach(prompts) { prompt in
                    Button {
                        use(prompt.text)
                        dismiss()
                    } label: {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(prompt.text).lineLimit(3).foregroundStyle(.primary)
                            if allSessions, let title = model.session(prompt.sessionId)?.title {
                                Text(title).font(.footnote).foregroundStyle(PhonePalette.muted)
                            }
                        }
                    }
                    .swipeActions(edge: .trailing) {
                        if !allSessions, SessionOperationAvailability.fork(session), prompt.turnId != nil {
                            Button("Fork from here") { fork(prompt) }.tint(PhonePalette.accentText)
                        }
                    }
                    .contextMenu {
                        Button { use(prompt.text); dismiss() } label: { Label("Use This Prompt", systemImage: "text.insert") }
                        if !allSessions, SessionOperationAvailability.fork(session), prompt.turnId != nil {
                            Button { fork(prompt) } label: { Label("Fork from Here", systemImage: "arrow.triangle.branch") }
                        }
                    }
                }
            }
            .searchable(text: $query, prompt: "Search what you sent")
            .navigationTitle("Recent Prompts")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Done") { dismiss() } } }
            .task(id: "\(allSessions)\u{0}\(query)") {
                loading = true
                try? await Task.sleep(for: .milliseconds(query.isEmpty ? 0 : 250))
                guard !Task.isCancelled else { return }
                let found = await model.recentPrompts(sessionID: allSessions ? nil : session.id, query: query.trimmingCharacters(in: .whitespacesAndNewlines))
                guard !Task.isCancelled else { return }
                var seen = Set<String>()
                prompts = found.prompts.filter { seen.insert($0.text).inserted }
                problems = found.problems
                loading = false
            }
        }
    }

    private func fork(_ prompt: PromptItem) {
        Task {
            do {
                try await model.fork(session, before: prompt)
                dismiss()
            } catch {
                problems = [AgenthailAPIError.userMessage(error)]
            }
        }
    }
}
