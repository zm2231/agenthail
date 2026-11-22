import SwiftUI

struct AgentAssignmentsSettings: View {
    var model: AgenthailModel

    var body: some View {
        AgentAssignmentsPage(store: model.agentRoles, sessions: model.snapshot?.sessions ?? []).agentRolesLoading(model)
    }
}

struct AgentAssignmentsPage: View {
    var store: AgentRolesStore
    let sessions: [SessionState]
    @State private var kind = AgentAssignmentKind.session
    @State private var session = ""
    @State private var typed = ""
    @State private var role = ""

    private var choices: [SessionState] {
        sessions.filter { $0.archived != true }.sorted { label($0).localizedCaseInsensitiveCompare(label($1)) == .orderedAscending }
    }

    private var target: String {
        let text = typed.trimmingCharacters(in: .whitespaces)
        return kind == .session && text.isEmpty ? session : text
    }

    var body: some View {
        VStack(spacing: 0) {
            AgentRolesNotices(store: store)
            Form {
                if let view = store.view {
                    Section("Assigned") {
                        if view.assignments.isEmpty {
                            Text("No session, identity or alias has an assigned role. Each acts under its usual role.").foregroundStyle(DesktopPalette.text2)
                        }
                        ForEach(view.assignments) { assignment in
                            AgentAssignmentRow(assignment: assignment, name: sessionName, locked: !store.editable) {
                                Task { await store.send(.unassign(assignment)) }
                            }
                        }
                    }
                    Section {
                        Picker("Kind", selection: $kind) {
                            ForEach(AgentAssignmentKind.allCases) { Text($0.title).tag($0) }
                        }
                        .pickerStyle(.segmented)
                        if kind == .session {
                            Picker("Session", selection: $session) {
                                Text("Choose a session").tag("")
                                ForEach(choices) { Text(label($0)).tag($0.id) }
                            }
                            .accessibilityIdentifier("agent-assign-session")
                        }
                        TextField(kind == .session ? "Or an id or alias" : (kind == .identity ? "Identity" : "Alias"), text: $typed, prompt: Text(kind == .session ? "@reviewer" : "reviewer"))
                            .accessibilityIdentifier("agent-assign-target")
                        Picker("Role", selection: $role) {
                            Text("Choose a role").tag("")
                            ForEach(view.roles) { Text($0.name).tag($0.name) }
                        }
                        .accessibilityIdentifier("agent-assign-role")
                        HStack {
                            if let error = store.saveError {
                                Text(error).foregroundStyle(DesktopPalette.failed).textSelection(.enabled)
                            }
                            Spacer()
                            Button("Assign") { Task { await assign() } }
                                .disabled(!store.editable || target.isEmpty || role.isEmpty)
                                .accessibilityIdentifier("agent-assign")
                        }
                        .font(.system(size: DesktopType.secondary))
                    } header: {
                        Text("Assign a role")
                    } footer: {
                        Text(footer)
                            .font(.system(size: DesktopType.secondary))
                            .foregroundStyle(DesktopPalette.text2)
                    }
                    .disabled(!store.editable)
                } else if store.loadError == nil {
                    Text("Reading agent roles.").foregroundStyle(DesktopPalette.text2)
                }
            }
            .formStyle(.grouped)
        }
        .onChange(of: kind) { typed = "" }
    }

    private var footer: String {
        switch kind {
        case .session: "The session acts under this role in place of its usual one."
        case .identity: "An agent identity holds the role wherever it runs."
        case .alias: "Whichever session holds the alias acts under this role."
        }
    }

    private func assign() async {
        if await store.send(.assign(role, kind: kind, target: target)) {
            typed = ""
            session = ""
        }
    }

    private func label(_ session: SessionState) -> String {
        session.alias.map { "@\($0)" } ?? session.name
    }

    private func sessionName(_ id: String) -> String? {
        sessions.first { $0.id == id }.map(label)
    }
}

private struct AgentAssignmentRow: View {
    let assignment: AgentRoleAssignment
    let name: (String) -> String?
    let locked: Bool
    let unassign: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
            VStack(alignment: .leading, spacing: 2) {
                Text(AgentRoleFacts.assignmentLine(assignment, sessionName: name))
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                Text(assignment.kind.capitalized)
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
                if let stale = assignment.stale {
                    Text("Applies to no session: \(stale)")
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.failed)
                }
            }
            Spacer()
            Text(assignment.role).foregroundStyle(DesktopPalette.text2)
            Button("Unassign", action: unassign).disabled(locked)
        }
        .accessibilityIdentifier("agent-assignment-" + assignment.target)
    }
}
