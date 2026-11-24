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
                    TextField("Description", text: Binding(get: { draft.definition.description ?? "" }, set: { draft.definition.description = $0 }), prompt: Text("What this role is for"), axis: .vertical)
                        .labelsHidden()
                        .multilineTextAlignment(.leading)
                        .lineLimit(1...4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Picker("Reach", selection: $draft.definition.reach) {
                        ForEach(AgentRoleFacts.reaches, id: \.id) { Text($0.title).tag($0.id) }
                    }
                } header: {
                    Text(role.name)
                }
                Section("Actions") {
                    ForEach(view.actions) { action in
                        AgentDecisionRow(action: action, defaultTitle: AgentRoleFacts.defaultTitle(action, in: role, reach: draft.definition.reach), draft: $draft)
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
                    Toggle("Default tools", isOn: Binding(get: { draft.defaultTools }, set: { draft.setDefaultTools($0, seen: role.tools ?? view.tools) }))
                    if draft.defaultTools {
                        Text(AgentRoleFacts.defaultToolsHint).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
                    } else {
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
        .onChange(of: role) { _, updated in draft.follow(updated) }
        .onChange(of: draft) { draft.follow(role) }
        .confirmationDialog(role.builtIn ? "Reset \(role.name) to how Agenthail ships it?" : "Delete \(role.name)?", isPresented: $confirmingRemove) {
            Button(role.builtIn ? "Reset" : "Delete", role: .destructive) { Task { await remove() } }
        } message: {
            Text(role.builtIn ? "Its actions, tools and limits go back to their defaults." : "Sessions assigned this role go back to their usual role.")
        }
    }

    private var footer: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            if draft.conflicts(with: role) {
                Label("\(role.name) changed elsewhere while you edited it. Save replaces that change; Revert shows it.", systemImage: "arrow.triangle.2.circlepath")
                    .foregroundStyle(DesktopPalette.text2)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("agent-role-conflict")
            }
            if let error = store.saveError {
                Label(error, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(DesktopPalette.failed)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("agent-role-error")
            }
            buttons
        }
        .font(.system(size: DesktopType.secondary))
        .padding(DesktopSpace.m)
    }

    private var buttons: some View {
        HStack(spacing: DesktopSpace.s) {
            Spacer()
            Button("New from This Role…", action: newFromThis).disabled(locked)
            if role.builtIn {
                Button("Reset") { confirmingRemove = true }.disabled(locked || !role.edited)
            } else {
                Button("Delete") { confirmingRemove = true }.disabled(locked)
            }
            Button("Revert") { draft = AgentRoleDraft(role); store.clearSaveError() }.disabled(!draft.changed)
            Button("Save") { Task { await save() } }
                .keyboardShortcut(.defaultAction)
                .disabled(locked || !draft.changed)
                .accessibilityIdentifier("agent-role-save")
        }
    }

    private func remove() async {
        if await store.send(.delete(role.name)), let reset = store.view?.role(role.name) { draft = AgentRoleDraft(reset) }
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
    let defaultTitle: String
    @Binding var draft: AgentRoleDraft

    var body: some View {
        let chosen = draft.decision(action.name)
        Picker(selection: Binding(get: { chosen }, set: { draft.setDecision($0, for: action) })) {
            Text(defaultTitle).tag(nil as AgentDecision?)
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
