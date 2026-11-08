import SwiftUI

struct SidebarProjectSection: View, @MainActor Equatable {
    let key: SidebarSectionKey
    let model: AgenthailModel
    let pane: SessionPane
    let toggleProject: () -> Void
    let toggleFamily: (String) -> Void

    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.key == rhs.key && lhs.model === rhs.model && lhs.pane === rhs.pane
    }

    var body: some View {
        let project = key.project
        let single = project.checkouts.count == 1 ? project.checkouts.first : nil
        VStack(alignment: .leading, spacing: 1) {
            ProjectHeaderView(name: project.name, hosts: project.hosts, branch: single?.branchLabel, dirty: single?.dirty ?? false, archived: project.archived, sessionCount: project.counts.sessions)
                .opensProject(project.identity?.id, model: model, pane: pane, shown: key.isShownProject)
                .contextMenu {
                    if let identity = project.identity {
                        Button(project.archived ? "Unarchive Project" : "Archive Project") { model.setProjectArchived(identity.id, !project.archived) }
                        if let state = model.projects.first(where: { $0.id == identity.id }) {
                            Divider()
                            ProjectMenuButtons(model: model, pane: pane, project: state)
                        }
                    }
                }
            ForEach(key.shown.checkouts) { checkout in
                if single == nil {
                    CheckoutRowView(checkout: checkout, host: project.spansHosts ? project.hostLabel(of: checkout) : nil)
                }
                ForEach(checkout.families) { family in
                    SidebarFamilyRows(key: key.family(family), model: model, pane: pane, toggle: { toggleFamily(family.id) })
                        .equatable()
                }
            }
            if let more = key.moreLabel {
                Button(more, action: toggleProject)
                    .buttonStyle(.plain)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.leading, 35)
                    .padding(.vertical, DesktopSpace.xs)
            }
        }
    }
}

struct SidebarFamilyRows: View, @MainActor Equatable {
    let key: SidebarFamilyKey
    let model: AgenthailModel
    let pane: SessionPane
    let toggle: () -> Void

    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.key == rhs.key && lhs.model === rhs.model && lhs.pane === rhs.pane
    }

    var body: some View {
        let family = key.family
        HStack(spacing: 0) {
            SidebarRowButton(session: family.root, title: family.root.title, family: family, attention: key.attention[family.root.id], selectedID: key.selectedID, model: model, pane: pane)
            if !family.members.isEmpty {
                Button(action: toggle) {
                    Image(systemName: key.expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: DesktopType.caption, weight: .semibold))
                        .frame(width: 18, height: 22)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .accessibilityLabel(key.expanded ? "Hide subagents" : "Show subagents")
            }
        }
        if key.expanded {
            ForEach(family.members) { member in
                SidebarRowButton(session: member.session, title: SessionFamilies.label(member.session), attention: key.attention[member.id], selectedID: key.selectedID, model: model, pane: pane)
                    .padding(.leading, CGFloat(member.depth) * 16)
            }
        }
    }
}

struct SidebarRowButton: View {
    let session: SessionState
    let title: String
    var family: SessionFamily? = nil
    let attention: SessionAttention?
    let selectedID: String?
    let model: AgenthailModel
    let pane: SessionPane

    var body: some View {
        SidebarSessionRow(
            state: SidebarRowState(session: session, title: title, family: family, attention: attention, finishedUnseen: model.finishedUnseen.contains(session.id), selected: selectedID == session.id, canMakeLead: ProjectMenu.canMakeLead(session, projects: model.projects)),
            actions: SidebarRowActions(shown: session, model: model, pane: pane)
        )
        .equatable()
    }
}
