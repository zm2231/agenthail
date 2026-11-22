import SwiftUI

struct ClaudeCompactSettings: View {
    var model: AgenthailModel
    @State private var store = ClaudeCompactStore()

    var body: some View {
        Form {
            Section {
                Text("When Claude compacts a conversation, for each Claude account folder. Claude's settings apply to sessions started after a change. Agenthail's idle compaction compacts a large idle conversation before its cache expires, and applies at once.")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
            }
            if let error = store.loadError {
                Section { Label(error, systemImage: "exclamationmark.triangle").foregroundStyle(DesktopPalette.failed).textSelection(.enabled) }
            }
            if let roots = store.roots {
                if roots.isEmpty {
                    Section { Text("No Claude accounts.").foregroundStyle(DesktopPalette.text2) }
                }
                ForEach(roots) { root in
                    Section {
                        ClaudeCompactRootFacts(root: root, holds: store.holds)
                    } header: {
                        Text(ClaudeCompactFacts.title(root.root)).help(root.settingsPath)
                    }
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
            Picker("Account folder", selection: $store.draft.root) {
                ForEach(roots) { Text(ClaudeCompactFacts.title($0.root)).tag($0.root) }
            }
            TextField("Model", text: $store.draft.model, prompt: Text("Blank for every model"))
            TextField("Window", text: $store.draft.window, prompt: Text("tokens, auto, or unset"))
            ClaudeCompactToggle(title: "Autocompact", value: $store.draft.enabled)
            ClaudeCompactToggle(title: "Idle compaction", value: $store.draft.idle)
            ClaudeCompactToggle(title: "Agenthail idle compaction", value: $store.draft.agenthailIdle)
            TextField("From context size", text: $store.draft.minTokens, prompt: Text("tokens, 200000 by default"))
            TextField("Before a usage limit, from", text: $store.draft.preLimit, prompt: Text("percent, 90 by default, or off"))
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
    @State private var showsHolds = false

    var body: some View {
        LabeledContent("Window", value: ClaudeCompactFacts.window(root.autoCompactWindow))
        ForEach(ClaudeCompactFacts.modelLines(root), id: \.model) { line in
            LabeledContent(line.model, value: line.window)
        }
        LabeledContent("Autocompact", value: ClaudeCompactFacts.toggle(root.autoCompactEnabled))
        LabeledContent("Idle compaction", value: ClaudeCompactFacts.toggle(root.idleCompaction))
        LabeledContent("Agenthail idle compaction", value: ClaudeCompactFacts.agenthailIdle(root.agenthailIdleCompaction))
        if let preLimit = ClaudeCompactFacts.preLimit(root.agenthailIdleCompaction) {
            LabeledContent("Before a usage limit", value: preLimit)
        }
        ForEach(ClaudeCompactFacts.overrideLines(root), id: \.self) { line in
            Text(line)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
                .textSelection(.enabled)
        }
        let held = ClaudeCompactFacts.holdLines(root, holds: holds)
        if !held.isEmpty {
            DisclosureGroup("Not compacted recently · \(held.count)", isExpanded: $showsHolds) {
                ForEach(Array(held.enumerated()), id: \.offset) { _, line in
                    Text(line)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
    }
}
