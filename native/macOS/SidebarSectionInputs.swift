import Foundation

/// Everything a project's block of the sidebar shows, so it redraws only when one of these changes.
struct SidebarSectionKey: Equatable {
    let project: SessionTree.Project
    let expanded: Bool
    let selectedID: String?
    let attention: [String: SessionAttention]
    let familyChoices: [String: Bool]
    let isShownProject: Bool

    init(project: SessionTree.Project, expanded: Bool, selectedID: String?, attention: [String: SessionAttention], familyChoices: [String: Bool], shownProjectID: String?) {
        self.project = project
        self.expanded = expanded
        isShownProject = shownProjectID != nil && project.identity?.id == shownProjectID
        self.selectedID = project.checkouts.contains { $0.families.contains { $0.contains(selectedID) } } ? selectedID : nil
        self.attention = attention
        self.familyChoices = familyChoices
    }

    var shown: SessionTree.Project { project.shown(expanded: expanded, keeping: selectedID) }
    var moreLabel: String? { project.moreLabel(expanded: expanded, keeping: selectedID) }

    func family(_ family: SessionFamily) -> SidebarFamilyKey {
        let ids = [family.root.id] + family.members.map(\.id)
        let selected = family.contains(selectedID) ? selectedID : nil
        return SidebarFamilyKey(
            family: family,
            expanded: family.isExpanded(chosen: familyChoices, selectedID: selected),
            tasksExpanded: family.tasksExpanded(chosen: familyChoices, selectedID: selected),
            selectedID: selected,
            attention: Dictionary(ids.compactMap { id in attention[id].map { (id, $0) } }, uniquingKeysWith: { first, _ in first })
        )
    }
}

struct SidebarFamilyKey: Equatable {
    let family: SessionFamily
    let expanded: Bool
    let tasksExpanded: Bool
    let selectedID: String?
    let attention: [String: SessionAttention]
}
