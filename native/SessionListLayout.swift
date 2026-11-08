import Foundation

enum SessionListLayout {
    static let groupsKey = "sessionList.groupsByProject"
    static let archivedKey = "sessionList.showsArchived"

    static func archivedRequest(filter: SessionFilter, refinement: SessionRefinement, shows: Bool) -> ScopeRequest? {
        guard shows, filter == .all else { return nil }
        return ScopeRequest(scope: SessionFilter.archived.viewScope, surface: refinement.surface, status: refinement.status?.rawValue)
    }

    static func archivedProjectID(_ id: String) -> String { "archived:" + id }

    static func shownFamilies(_ tree: SessionTree, grouped: Bool, projectExpanded: (String) -> Bool, keeping selectedID: String?) -> [SessionFamily] {
        guard grouped else { return tree.flat }
        return tree.projects.flatMap { $0.shown(expanded: projectExpanded($0.id), keeping: selectedID).checkouts.flatMap(\.families) }
    }

    static func order(_ families: [SessionFamily], expanded: (SessionFamily) -> Bool) -> [String] {
        var ids: [String] = []
        for family in families {
            let rows = [family.root.id] + (expanded(family) ? family.members.map(\.id) : [])
            ids += rows.filter { !ids.contains($0) }
        }
        return ids
    }
}
