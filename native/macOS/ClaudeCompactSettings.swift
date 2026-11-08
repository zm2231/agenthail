import SwiftUI

struct ClaudeCompactSettings: View {
    var model: AgenthailModel
    @State private var store = ClaudeCompactStore()

    var body: some View {
        Form {
            Section {
                Text("When Claude compacts a conversation, per Claude config folder. Claude's own settings apply to sessions started after the change. Agenthail's idle compaction compacts a large idle conversation before its cache expires, even when the account is near its limit, and applies at once.")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
            }
            if let error = store.loadError {
                Section { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(DesktopPalette.failed).textSelection(.enabled) }
            }
            if let roots = store.roots {
                if roots.isEmpty {
                    Section { Text("No Claude config folders.").foregroundStyle(DesktopPalette.text2) }
                }
                ForEach(roots) { root in
                    Section(root.settingsPath) { ClaudeCompactRootFacts(root: root, holds: store.holds) }
                }
                if !roots.isEmpty { changeSection(roots) }
            } else if store.loadError == nil {
                Section { Text("Reading Claude settings.").foregroundStyle(DesktopPalette.text2) }
            }
        }
        .formStyle(.grouped)
        .task(id: model.isConnected) { await store.load(model.api) }
    }

    private func changeSection(_ roots: [ClaudeCompactRoot]) -> some View {
        Section("Change") {
            Picker("Config folder", selection: $store.draft.root) {
                ForEach(roots) { Text($0.root).tag($0.root) }
            }
            TextField("Model", text: $store.draft.model, prompt: Text("Blank for every model"))
            TextField("Window", text: $store.draft.window, prompt: Text("tokens, auto, or unset"))
            ClaudeCompactToggle(title: "Autocompact", value: $store.draft.enabled)
            ClaudeCompactToggle(title: "Idle compaction", value: $store.draft.idle)
            ClaudeCompactToggle(title: "Agenthail idle compaction", value: $store.draft.agenthailIdle)
            TextField("From context size", text: $store.draft.minTokens, prompt: Text("tokens, 200000 by default"))
            HStack {
                if let problem = store.draft.problem ?? store.saveError {
                    Text(problem).foregroundStyle(DesktopPalette.failed).textSelection(.enabled)
                } else if let notice = store.notice {
                    Text(notice).foregroundStyle(DesktopPalette.text2)
                }
                Spacer()
                Button("Save") { Task { await store.save(model.api) } }
                    .disabled(store.saving || store.draft.problem != nil || !store.draft.changes)
            }
            .font(.system(size: DesktopType.secondary))
        }
    }
}

private struct ClaudeCompactToggle: View {
    let title: String
    @Binding var value: Bool?

    var body: some View {
        Picker(title, selection: $value) {
            Text("Leave as is").tag(nil as Bool?)
            Text("On").tag(Optional(true))
            Text("Off").tag(Optional(false))
        }
    }
}

private struct ClaudeCompactRootFacts: View {
    let root: ClaudeCompactRoot
    let holds: [ClaudeCompactHold]

    var body: some View {
        LabeledContent("Window", value: ClaudeCompactFacts.window(root.autoCompactWindow))
        ForEach(ClaudeCompactFacts.modelLines(root), id: \.self) { Text($0) }
        LabeledContent("Autocompact", value: ClaudeCompactFacts.toggle(root.autoCompactEnabled))
        LabeledContent("Idle compaction", value: ClaudeCompactFacts.toggle(root.idleCompaction))
        LabeledContent("Agenthail idle compaction", value: ClaudeCompactFacts.agenthailIdle(root.agenthailIdleCompaction))
        ForEach(Array((ClaudeCompactFacts.holdLines(root, holds: holds) + ClaudeCompactFacts.overrideLines(root)).enumerated()), id: \.offset) { _, line in
            Text(line)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
                .textSelection(.enabled)
        }
    }
}
