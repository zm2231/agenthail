import SwiftUI

struct AgentRoleDetail: View {
    var store: AgentRolesStore
    let view: AgentRolesView
    let role: AgentRoleView
    let newFromThis: () -> Void
    @State private var draft: AgentRoleDraft
    @State private var confirmingRemove = false

    init(store: AgentRolesStore, view: AgentRolesView, role: AgentRoleView, newFromThis: @escaping () -> Void) {
        self.store = store
        self.view = view
        self.role = role
        self.newFromThis = newFromThis
        _draft = State(initialValue: AgentRoleDraft(role))
    }

    private var locked: Bool { !store.editable }

    var body: some View {
        VStack(spacing: 0) {
            Form {
                Section {
                    TextField("Description", text: Binding(get: { draft.definition.description ?? "" }, set: { draft.definition.description = $0 }), axis: .vertical)
                        .lineLimit(1...3)
                    Picker("Reach", selection: $draft.definition.reach) {
                        ForEach(AgentRoleFacts.reaches, id: \.id) { Text($0.title).tag($0.id) }
                    }
                } header: {
                    Text(role.name)
                }
                Section("Actions") {
                    ForEach(view.actions) { action in
                        AgentDecisionRow(action: action, effectiveDefault: AgentRoleFacts.effectiveDefault(action, in: role), draft: $draft)
                    }
                }
                Section("Limits") {
                    Stepper(value: $draft.depth, in: 1...20) {
                        LabeledContent("Spawn depth", value: limitLabel(draft.depth, AgentRoleFacts.defaultDepth))
                    }
                    Stepper(value: $draft.running, in: 1...64) {
                        LabeledContent("Running sessions", value: limitLabel(draft.running, AgentRoleFacts.defaultRunning))
                    }
                }
                Section("Agenthail tools") {
                    Toggle("Every tool", isOn: Binding(get: { draft.allTools }, set: { draft.setAllTools($0, every: view.tools) }))
                    if !draft.allTools {
                        ForEach(view.tools, id: \.self) { tool in
                            Toggle(tool, isOn: Binding(get: { draft.sees(tool) }, set: { draft.setTool(tool, shown: $0, every: view.tools) }))
                                .font(.system(size: DesktopType.secondary, design: .monospaced))
                        }
                    }
                }
            }
            .formStyle(.grouped)
            .disabled(locked)
            Divider()
            footer
        }
        .onChange(of: role) { _, updated in
            if !draft.changed { draft = AgentRoleDraft(updated) }
        }
        .confirmationDialog(role.builtIn ? "Reset \(role.name) to how Agenthail ships it?" : "Delete \(role.name)?", isPresented: $confirmingRemove) {
            Button(role.builtIn ? "Reset" : "Delete", role: .destructive) { Task { await store.send(.delete(role.name)) } }
        } message: {
            Text(role.builtIn ? "Its actions, tools and limits go back to their defaults." : "Sessions assigned this role go back to their usual role.")
        }
    }

    private var footer: some View {
        HStack(spacing: DesktopSpace.s) {
            if let error = store.saveError {
                Text(error)
                    .foregroundStyle(DesktopPalette.failed)
                    .textSelection(.enabled)
                    .lineLimit(3)
                    .accessibilityIdentifier("agent-role-error")
            }
            Spacer()
            Button("New from This Role…", action: newFromThis).disabled(locked)
            if role.builtIn {
                Button("Reset") { confirmingRemove = true }.disabled(locked || !role.edited)
            } else {
                Button("Delete") { confirmingRemove = true }.disabled(locked)
            }
            Button("Revert") { draft.revert(); store.clearSaveError() }.disabled(!draft.changed)
            Button("Save") { Task { await save() } }
                .keyboardShortcut(.defaultAction)
                .disabled(locked || !draft.changed)
                .accessibilityIdentifier("agent-role-save")
        }
        .font(.system(size: DesktopType.secondary))
        .padding(DesktopSpace.m)
    }

    private func save() async {
        if await store.send(draft.request), let saved = store.view?.role(role.name) { draft = AgentRoleDraft(saved) }
    }

    private func limitLabel(_ value: Int, _ fallback: Int) -> String {
        value == fallback ? "\(value) (default)" : "\(value)"
    }
}

private struct AgentDecisionRow: View {
    let action: AgentActionView
    let effectiveDefault: String
    @Binding var draft: AgentRoleDraft

    var body: some View {
        let chosen = draft.decision(action.name)
        Picker(selection: Binding(get: { chosen }, set: { draft.setDecision($0, for: action, effectiveDefault: effectiveDefault) })) {
            Text("Default (\(effectiveDefault.capitalized))").tag(nil as AgentDecision?)
            ForEach(AgentDecision.allCases, id: \.self) { Text($0.title).tag(Optional($0)) }
        } label: {
            HStack(spacing: DesktopSpace.xs) {
                Circle()
                    .fill(DesktopPalette.accent.when(chosen != nil))
                    .frame(width: 6, height: 6)
                    .accessibilityLabel(chosen != nil ? "Changed" : "")
                VStack(alignment: .leading, spacing: 1) {
                    Text(action.detail)
                    Text(action.name)
                        .font(.system(size: DesktopType.caption, design: .monospaced))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
        }
        .accessibilityIdentifier("agent-decision-" + action.name)
    }
}

struct AgentRoleNewSheet: View {
    var store: AgentRolesStore
    let from: AgentRoleView
    let taken: [String]
    let created: (String) -> Void
    @State private var name = ""
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text("New role from \(from.name)").font(.system(size: DesktopType.title, weight: .semibold))
            TextField("Name", text: $name, prompt: Text("reviewer"))
                .onSubmit(create)
                .accessibilityIdentifier("agent-role-new-name")
            if let hint = AgentRoleFacts.nameHint(name, taken: taken) {
                Text(hint).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
            }
            if let error = store.saveError {
                Text(error).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.failed).textSelection(.enabled)
            }
            HStack {
                Spacer()
                Button("Cancel") { store.clearSaveError(); dismiss() }.keyboardShortcut(.cancelAction)
                Button("Create", action: create)
                    .keyboardShortcut(.defaultAction)
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || store.busy)
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 360)
        .onAppear { store.clearSaveError() }
    }

    private func create() {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return }
        Task {
            if await store.send(AgentRoleDraft(name: trimmed, copying: from.definition).request) {
                created(trimmed)
                dismiss()
            }
        }
    }
}
