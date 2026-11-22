import SwiftUI

struct IOSAgentRoleScreen: View {
    var store: AgentRolesStore
    let name: String
    @State private var draft: AgentRoleDraft?
    @State private var confirmingRemove = false
    @State private var creating = false
    @State private var newName = ""
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        Group {
            if let view = store.view, let role = view.role(name) {
                form(view, role)
                    .onAppear { if draft == nil { draft = AgentRoleDraft(role) } }
                    .onChange(of: role) { _, updated in
                        if draft?.changed != true { draft = AgentRoleDraft(updated) }
                    }
            } else {
                Text("This role is gone.").foregroundStyle(PhonePalette.muted)
            }
        }
        .navigationTitle(name)
        .navigationBarTitleDisplayMode(.inline)
    }

    private func form(_ view: AgentRolesView, _ role: AgentRoleView) -> some View {
        let binding = Binding(get: { draft ?? AgentRoleDraft(role) }, set: { draft = $0 })
        let current = binding.wrappedValue
        return Form {
            IOSAgentRolesNotices(store: store)
            if let error = store.saveError {
                Text(error).foregroundStyle(PhonePalette.failed).textSelection(.enabled).accessibilityIdentifier("agent-role-error")
            }
            Section {
                TextField("Description", text: Binding(get: { current.definition.description ?? "" }, set: { binding.wrappedValue.definition.description = $0 }), axis: .vertical)
                    .lineLimit(1...4)
                Picker("Reach", selection: binding.definition.reach) {
                    ForEach(AgentRoleFacts.reaches, id: \.id) { Text($0.title).tag($0.id) }
                }
            } footer: {
                if let host = store.hostName { Text("Changes apply on \(host).") }
            }
            .disabled(!store.editable)
            Section("Actions") {
                ForEach(view.actions) { action in
                    IOSAgentDecisionRow(action: action, effectiveDefault: AgentRoleFacts.effectiveDefault(action, in: role), draft: binding)
                }
            }
            .disabled(!store.editable)
            Section("Limits") {
                Stepper(value: binding.depth, in: 1...20) {
                    LabeledContent("Spawn depth", value: limitLabel(current.depth, AgentRoleFacts.defaultDepth))
                }
                Stepper(value: binding.running, in: 1...64) {
                    LabeledContent("Running sessions", value: limitLabel(current.running, AgentRoleFacts.defaultRunning))
                }
            }
            .disabled(!store.editable)
            Section("Agenthail tools") {
                Toggle("Every tool", isOn: Binding(get: { current.allTools }, set: { binding.wrappedValue.setAllTools($0, every: view.tools) }))
                if !current.allTools {
                    ForEach(view.tools, id: \.self) { tool in
                        Toggle(tool, isOn: Binding(get: { current.sees(tool) }, set: { binding.wrappedValue.setTool(tool, shown: $0, every: view.tools) }))
                            .font(.callout.monospaced())
                    }
                }
            }
            .disabled(!store.editable)
            Section {
                Button("New from This Role…") { newName = ""; store.clearSaveError(); creating = true }
                if role.builtIn {
                    Button("Reset to How It Ships", role: .destructive) { confirmingRemove = true }.disabled(!role.edited)
                } else {
                    Button("Delete Role", role: .destructive) { confirmingRemove = true }
                }
            }
            .disabled(!store.editable)
        }
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") { Task { if await store.send(current.request) { draft = nil } } }
                    .disabled(!store.editable || !current.changed)
                    .accessibilityIdentifier("agent-role-save")
            }
            if current.changed {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Revert") { draft = AgentRoleDraft(role); store.clearSaveError() }
                }
            }
        }
        .confirmationDialog(role.builtIn ? "Reset \(role.name)?" : "Delete \(role.name)?", isPresented: $confirmingRemove, titleVisibility: .visible) {
            Button(role.builtIn ? "Reset" : "Delete", role: .destructive) {
                Task {
                    if await store.send(.delete(role.name)), !role.builtIn { dismiss() }
                }
            }
        } message: {
            Text(role.builtIn ? "Its actions, tools and limits go back to their defaults." : "Sessions assigned this role go back to their usual role.")
        }
        .alert("New Role from \(role.name)", isPresented: $creating) {
            TextField("Name", text: $newName)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            Button("Cancel", role: .cancel) {}
            Button("Create") {
                let trimmed = newName.trimmingCharacters(in: .whitespaces)
                Task { await store.send(AgentRoleDraft(name: trimmed, copying: role.definition).request) }
            }
        } message: {
            Text(AgentRoleFacts.nameHint(newName, taken: view.roles.map(\.name)) ?? "Lowercase letters, digits and dashes. It starts with this role's settings.")
        }
    }

    private func limitLabel(_ value: Int, _ fallback: Int) -> String {
        value == fallback ? "\(value) (default)" : "\(value)"
    }
}

private struct IOSAgentDecisionRow: View {
    let action: AgentActionView
    let effectiveDefault: String
    @Binding var draft: AgentRoleDraft

    var body: some View {
        let chosen = draft.decision(action.name)
        Picker(selection: Binding(get: { chosen }, set: { draft.setDecision($0, for: action, effectiveDefault: effectiveDefault) })) {
            Text("Default (\(effectiveDefault.capitalized))").tag(nil as AgentDecision?)
            ForEach(AgentDecision.allCases, id: \.self) { Text($0.title).tag(Optional($0)) }
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                Text(action.detail)
                if chosen != nil {
                    Text("Changed").font(.footnote).foregroundStyle(PhonePalette.accentText)
                }
            }
        }
        .accessibilityIdentifier("agent-decision-" + action.name)
    }
}
