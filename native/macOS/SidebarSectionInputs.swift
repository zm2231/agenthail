import Foundation

/// Everything a project's block of the sidebar shows, so it redraws only when one of these changes.
struct SidebarSectionKey: Equatable {
    let project: SessionTree.Project
    let expanded: Bool
    let selectedID: String?
    let attention: [String: SessionAttention]
    let expandedFamilies: Set<String>

    init(project: SessionTree.Project, expanded: Bool, selectedID: String?, attention: [String: SessionAttention], expandedFamilies: Set<String>) {
        self.project = project
        self.expanded = expanded
        self.selectedID = project.checkouts.contains { $0.families.contains { $0.contains(selectedID) } } ? selectedID : nil
        self.attention = attention
        self.expandedFamilies = expandedFamilies
    }

    var shown: SessionTree.Project { project.shown(expanded: expanded, keeping: selectedID) }
    var moreLabel: String? { project.moreLabel(expanded: expanded, keeping: selectedID) }

    func family(_ family: SessionFamily) -> SidebarFamilyKey {
        let ids = [family.root.id] + family.members.map(\.id)
        let selected = family.contains(selectedID) ? selectedID : nil
        return SidebarFamilyKey(
            family: family,
            expanded: !family.members.isEmpty && (expandedFamilies.contains(family.id) || family.members.contains { $0.id == selected }),
            selectedID: selected,
            attention: Dictionary(ids.compactMap { id in attention[id].map { (id, $0) } }, uniquingKeysWith: { first, _ in first })
        )
    }
}

struct SidebarFamilyKey: Equatable {
    let family: SessionFamily
    let expanded: Bool
    let selectedID: String?
    let attention: [String: SessionAttention]
}
