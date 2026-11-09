import SwiftUI

struct SidebarFlatFamilies: View {
    let families: [SessionFamily]
    let attention: [String: SessionAttention]
    let model: AgenthailModel
    let pane: SessionPane
    @Binding var toggledFamilies: Set<String>

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            ForEach(families) { family in
                let expanded = family.isExpanded(toggled: toggledFamilies, selectedID: pane.selectedSessionID)
                SidebarFamilyRows(key: SidebarFamilyKey(family: family, expanded: expanded, selectedID: pane.selectedSessionID, attention: attention), model: model, pane: pane) {
                    if toggledFamilies.contains(family.id) { toggledFamilies.remove(family.id) } else { toggledFamilies.insert(family.id) }
                }
                .equatable()
            }
        }
    }
}

struct SidebarArchivedSection: View {
    let model: AgenthailModel
    let pane: SessionPane
    let grouped: Bool
    @Binding var expandedProjects: Set<String>
    @Binding var toggledFamilies: Set<String>

    var body: some View {
        if let request = model.archivedRequest {
            let tree = model.archivedTree()
            VStack(alignment: .leading, spacing: 2) {
                SidebarCaption("Archived")
                if grouped {
                    ForEach(tree.projects) { project in
                        let id = SessionListLayout.archivedProjectID(project.id)
                        SidebarProjectSection(
                            key: SidebarSectionKey(project: project, expanded: expandedProjects.contains(id), selectedID: pane.selectedSessionID, attention: tree.attention, toggledFamilies: toggledFamilies, shownProjectID: pane.shownProjectID),
                            model: model,
                            pane: pane,
                            toggleProject: { flip(id, in: $expandedProjects) },
                            toggleFamily: { flip($0, in: $toggledFamilies) }
                        )
                        .equatable()
                    }
                } else {
                    SidebarFlatFamilies(families: tree.flat, attention: tree.attention, model: model, pane: pane, toggledFamilies: $toggledFamilies)
                }
                if model.archivedHasMore {
                    ProgressView()
                        .controlSize(.small)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, DesktopSpace.s)
                        .task(id: ListPageKey(request: request, rows: model.pages.listings[request]?.rows.count ?? -1)) { await model.readArchived() }
                } else if tree.flat.isEmpty {
                    Text("Nothing is archived.")
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(.horizontal, DesktopSpace.s)
                }
                if let error = model.reader.pageErrors[request] {
                    Text(error)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(.horizontal, DesktopSpace.s)
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("sidebar-archived")
        }
    }

    private func flip(_ id: String, in set: Binding<Set<String>>) {
        if set.wrappedValue.contains(id) { set.wrappedValue.remove(id) } else { set.wrappedValue.insert(id) }
    }
}
