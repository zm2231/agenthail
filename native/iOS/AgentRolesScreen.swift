import SwiftUI

struct AgentsSettingsSection: View {
    var model: AgenthailIOSModel

    var body: some View {
        Section {
            NavigationLink("Roles") { IOSAgentRolesScreen(model: model) }
                .accessibilityIdentifier("agent-roles")
            NavigationLink("Assignments") { IOSAgentAssignmentsScreen(model: model) }
                .accessibilityIdentifier("agent-assignments")
        } header: {
            Text("Agents")
        } footer: {
            if let host = model.agentRolesHost {
                Text("What agents may do without asking you. Changes apply on \(host.name).")
            } else {
                Text("Connect a computer to see its agent roles.")
            }
        }
    }
}

struct IOSAgentRolesScreen: View {
    var model: AgenthailIOSModel

    var body: some View {
        let store = model.agentRoles
        List {
            IOSAgentRolesNotices(store: store)
            if let view = store.view {
                Section {
                    ForEach(view.roles) { role in
                        NavigationLink {
                            IOSAgentRoleScreen(store: store, name: role.name)
                        } label: {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(role.name)
                                Text(role.builtIn ? (role.edited ? "Built in, changed" : "Built in") : "Yours")
                                    .font(.footnote)
                                    .foregroundStyle(role.edited ? AnyShapeStyle(PhonePalette.accentText) : AnyShapeStyle(PhonePalette.muted))
                            }
                        }
                        .accessibilityIdentifier("agent-role-" + role.name)
                    }
                } footer: {
                    if let host = store.hostName { Text("Roles on \(host).") }
                }
            } else if store.loadError == nil {
                Text("Reading agent roles.").foregroundStyle(PhonePalette.muted)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Roles")
        .navigationBarTitleDisplayMode(.inline)
        .agentRolesLoading(model)
    }
}

struct IOSAgentRolesNotices: View {
    var store: AgentRolesStore

    var body: some View {
        if let error = store.loadError {
            Text(error).foregroundStyle(PhonePalette.failed).textSelection(.enabled)
        }
        if let reason = store.readOnlyReason {
            Text(reason).foregroundStyle(PhonePalette.muted)
        }
    }
}

struct IOSAgentAssignmentsScreen: View {
    var model: AgenthailIOSModel
    @State private var adding = false

    var body: some View {
        let store = model.agentRoles
        List {
            IOSAgentRolesNotices(store: store)
            if let error = store.saveError, !adding {
                Text(error).foregroundStyle(PhonePalette.failed).textSelection(.enabled).accessibilityIdentifier("agent-assignments-error")
            }
            if let view = store.view {
                Section {
                    if view.assignments.isEmpty {
                        Text("No session, identity or alias has an assigned role. Each acts under its usual role.").foregroundStyle(PhonePalette.muted)
                    }
                    ForEach(view.assignments) { assignment in
                        VStack(alignment: .leading, spacing: 2) {
                            LabeledContent(AgentRoleFacts.assignmentLine(assignment, sessionName: sessionName), value: assignment.role)
                            Text(assignment.kind.capitalized).font(.footnote).foregroundStyle(PhonePalette.muted)
                            if let stale = assignment.stale {
                                Text("Applies to no session: \(stale)").font(.footnote).foregroundStyle(PhonePalette.failed)
                            }
                        }
                        .accessibilityIdentifier("agent-assignment-" + assignment.target)
                        .swipeActions {
                            if store.editable {
                                Button("Unassign", role: .destructive) { Task { await store.send(.unassign(assignment)) } }
                            }
                        }
                    }
                } footer: {
                    if let host = store.hostName { Text("Assignments on \(host). Swipe to unassign.") }
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Assignments")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Assign", systemImage: "plus") { store.clearSaveError(); adding = true }
                    .disabled(!store.editable)
                    .accessibilityIdentifier("agent-assign-open")
            }
        }
        .sheet(isPresented: $adding) { IOSAgentAssignSheet(model: model) }
        .agentRolesLoading(model)
    }

    private func sessionName(_ id: String) -> String? {
        guard let host = model.agentRolesHost?.id else { return nil }
        let name = model.groupName(id, on: host)
        return name == id ? nil : name
    }
}

private struct IOSAgentAssignSheet: View {
    var model: AgenthailIOSModel
    @State private var kind = AgentAssignmentKind.session
    @State private var session = ""
    @State private var typed = ""
    @State private var role = ""
    @Environment(\.dismiss) private var dismiss

    private var target: String {
        let text = typed.trimmingCharacters(in: .whitespaces)
        return kind == .session && text.isEmpty ? session : text
    }

    var body: some View {
        let store = model.agentRoles
        NavigationStack {
            Form {
                Picker("Kind", selection: $kind) {
                    ForEach(AgentAssignmentKind.allCases) { Text($0.title).tag($0) }
                }
                .pickerStyle(.segmented)
                if kind == .session, let host = model.agentRolesHost {
                    IOSGroupSessionPicker(model: model, title: "Session", hostID: host.id, selection: $session)
                        .accessibilityIdentifier("agent-assign-session")
                }
                TextField(kind == .session ? "Or an id or @alias" : (kind == .identity ? "Identity" : "Alias"), text: $typed)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("agent-assign-target")
                Picker("Role", selection: $role) {
                    Text("Choose a role").tag("")
                    ForEach(store.view?.roles ?? []) { Text($0.name).tag($0.name) }
                }
                .accessibilityIdentifier("agent-assign-role")
                if let error = store.saveError {
                    Text(error).foregroundStyle(PhonePalette.failed).textSelection(.enabled)
                }
            }
            .navigationTitle("Assign a Role")
            .navigationBarTitleDisplayMode(.inline)
            .onChange(of: kind) { typed = "" }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { store.clearSaveError(); dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Assign") {
                        Task { if await store.send(.assign(role, kind: kind, target: target)) { dismiss() } }
                    }
                    .disabled(!store.editable || target.isEmpty || role.isEmpty)
                    .accessibilityIdentifier("agent-assign")
                }
            }
        }
    }
}

private struct IOSAgentRolesLoading: ViewModifier {
    var model: AgenthailIOSModel

    func body(content: Content) -> some View {
        content
            .task(id: model.agentRolesHost?.id) {
                model.attachAgentRoles()
                await model.agentRoles.load()
            }
            .refreshable { await model.agentRoles.load() }
    }
}

extension View {
    func agentRolesLoading(_ model: AgenthailIOSModel) -> some View { modifier(IOSAgentRolesLoading(model: model)) }
}
