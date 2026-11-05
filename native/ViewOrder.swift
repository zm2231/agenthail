import Foundation

struct ViewProject: Decodable, Equatable, Identifiable {
    let id: String
    let name: String
    let counts: ScopePage.Count
    let collapsedFamilyIds: [String]
}

struct ViewMember: Decodable, Equatable, Identifiable {
    let id: String
    let depth: Int
    let contextOnly: Bool
    let pinned: Bool
}

struct ViewFamily: Decodable, Equatable, Identifiable {
    let id: String
    let rootId: String
    let projectId: String
    let checkoutId: String
    let members: [ViewMember]
    let pinned: Bool
    let workingSubagents: Int
}

extension ScopePage.View {
    func merging(_ next: ScopePage.View?) -> ScopePage.View {
        guard let next else { return self }
        var merged = self
        let held = Set(families.map(\.id))
        merged.families += next.families.filter { !held.contains($0.id) }
        return merged
    }

    var memberIDs: Set<String> { Set(families.flatMap { $0.members.map(\.id) }) }

    func renamed(_ name: (String) -> String) -> ScopePage.View {
        var copy = self
        copy.projects = projects.map { ViewProject(id: name($0.id), name: $0.name, counts: $0.counts, collapsedFamilyIds: $0.collapsedFamilyIds.map(name)) }
        copy.families = families.map { family in
            ViewFamily(id: name(family.id), rootId: name(family.rootId), projectId: name(family.projectId), checkoutId: family.checkoutId.isEmpty ? "" : name(family.checkoutId), members: family.members.map { ViewMember(id: name($0.id), depth: $0.depth, contextOnly: $0.contextOnly, pinned: $0.pinned) }, pinned: family.pinned, workingSubagents: family.workingSubagents)
        }
        return copy
    }

    /// One computer's order first; another's projects and families it lacks follow in that computer's order.
    static func joined(_ views: [ScopePage.View]) -> ScopePage.View? {
        guard var joined = views.first else { return nil }
        for view in views.dropFirst() {
            let projects = Set(joined.projects.map(\.id))
            let families = Set(joined.families.map(\.id))
            if !view.families.contains(where: { families.contains($0.id) }) {
                for (scope, count) in view.counts {
                    let held = joined.counts[scope] ?? ScopePage.Count(families: 0, sessions: 0)
                    joined.counts[scope] = ScopePage.Count(families: held.families + count.families, sessions: held.sessions + count.sessions)
                }
            }
            joined.projects += view.projects.filter { !projects.contains($0.id) }
            joined.families += view.families.filter { !families.contains($0.id) }
            for project in view.projects where projects.contains(project.id) {
                guard let index = joined.projects.firstIndex(where: { $0.id == project.id }) else { continue }
                let held = joined.projects[index]
                joined.projects[index] = ViewProject(id: held.id, name: held.name, counts: ScopePage.Count(families: held.counts.families + project.counts.families, sessions: held.counts.sessions + project.counts.sessions), collapsedFamilyIds: held.collapsedFamilyIds + project.collapsedFamilyIds.filter { !families.contains($0) })
            }
        }
        return joined
    }
}
