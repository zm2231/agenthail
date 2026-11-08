import SwiftUI

struct SidebarFlatFamilies: View {
    let families: [SessionFamily]
    let attention: [String: SessionAttention]
    let model: AgenthailModel
    let pane: SessionPane
    @Binding var expandedFamilies: Set<String>

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            ForEach(families) { family in
                let expanded = !family.members.isEmpty && (expandedFamilies.contains(family.id) || family.members.contains { $0.id == pane.selectedSessionID })
                SidebarFamilyRows(key: SidebarFamilyKey(family: family, expanded: expanded, selectedID: pane.selectedSessionID, attention: attention), model: model, pane: pane) {
                    if expandedFamilies.contains(family.id) { expandedFamilies.remove(family.id) } else { expandedFamilies.insert(family.id) }
                }
                .equatable()
            }
        }
    }
}

struct SidebarArchivedSection: View {
    let model: AgenthailModel
    let pane: SessionPane
    @Binding var expandedFamilies: Set<String>

    var body: some View {
        if let request = model.archivedRequest {
            let tree = model.archivedTree()
            VStack(alignment: .leading, spacing: 2) {
                SidebarCaption("Archived")
                SidebarFlatFamilies(families: tree.flat, attention: tree.attention, model: model, pane: pane, expandedFamilies: $expandedFamilies)
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
}
