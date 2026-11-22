import SwiftUI

struct AgentRolesSettings: View {
    var model: AgenthailModel
    @State private var selected: String?
    @State private var creating = false

    private var store: AgentRolesStore { model.agentRoles }

    var body: some View {
        VStack(spacing: 0) {
            AgentRolesNotices(store: store)
            if let view = store.view {
                HStack(spacing: 0) {
                    List(view.roles, selection: $selected) { role in
                        AgentRoleListRow(role: role).tag(role.name)
                    }
                    .frame(width: 180)
                    .accessibilityIdentifier("agent-roles-list")
                    Divider()
                    if let role = view.role(selected ?? "") {
                        AgentRoleDetail(store: store, view: view, role: role) { creating = true }
                            .id(role.name)
                    } else {
                        Text("Choose a role.").foregroundStyle(DesktopPalette.text2).frame(maxWidth: .infinity, maxHeight: .infinity)
                    }
                }
                .sheet(isPresented: $creating) {
                    if let role = view.role(selected ?? "") {
                        AgentRoleNewSheet(store: store, from: role, taken: view.roles.map(\.name)) { selected = $0 }
                    }
                }
            } else if store.loadError == nil {
                Text("Reading agent roles.").foregroundStyle(DesktopPalette.text2).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Spacer()
            }
        }
        .agentRolesLoading(model)
        .onChange(of: store.view?.roles.map(\.name)) { _, names in
            if let names, selected == nil || !names.contains(selected ?? "") { selected = names.first }
        }
        .onAppear { if selected == nil { selected = store.view?.roles.first?.name } }
    }
}

struct AgentRolesNotices: View {
    var store: AgentRolesStore

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            if let reason = store.readOnlyReason {
                Label(reason, systemImage: "lock")
                    .foregroundStyle(DesktopPalette.text2)
                    .accessibilityIdentifier("agent-roles-read-only")
            }
            if let error = store.loadError {
                Label(error, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(DesktopPalette.failed)
                    .textSelection(.enabled)
            }
        }
        .font(.system(size: DesktopType.secondary))
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, DesktopSpace.l)
        .padding(.vertical, store.readOnlyReason == nil && store.loadError == nil ? 0 : DesktopSpace.s)
    }
}

private struct AgentRoleListRow: View {
    let role: AgentRoleView

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(role.name).font(.system(size: DesktopType.body))
            Text(role.builtIn ? (role.edited ? "Built in, changed" : "Built in") : "Yours")
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(role.edited ? AnyShapeStyle(DesktopPalette.accentText) : AnyShapeStyle(DesktopPalette.text2))
        }
        .accessibilityIdentifier("agent-role-" + role.name)
    }
}

private struct AgentRolesLoading: ViewModifier {
    var model: AgenthailModel

    func body(content: Content) -> some View {
        content.task(id: model.isConnected) {
            model.agentRoles.attach(MacAgentRolesTransport(socket: .agenthail, api: model.api))
            await model.agentRoles.load()
        }
    }
}

extension View {
    func agentRolesLoading(_ model: AgenthailModel) -> some View { modifier(AgentRolesLoading(model: model)) }
}
