import Foundation

extension SessionTree {
    static func ordered(list listed: ScopePage.View?, current: ScopePage.View?, rows: [String: SessionState], attention: [String: SessionAttention], cells: [String: SessionCell] = [:]) -> SessionTree {
        var projects: [Project] = []
        for viewProject in listed?.projects ?? [] {
            let families = (listed?.families ?? []).filter { $0.projectId == viewProject.id }.compactMap { family(from: $0, rows: rows, cells: cells) }
            guard let first = families.first else { continue }
            var project = Project(id: viewProject.id, name: viewProject.name, identity: first.root.project, checkouts: [])
            project.collapsed = Set(viewProject.collapsedFamilyIds)
            project.counts = viewProject.counts
            for family in families {
                let checkoutID = checkoutID(of: family.root, view: listed?.families.first { $0.id == family.id })
                if let index = project.checkouts.firstIndex(where: { $0.id == checkoutID }) {
                    project.checkouts[index].families.append(family)
                } else {
                    let root = family.root
                    project.checkouts.append(Checkout(id: checkoutID, host: root.remoteHostName == nil ? nil : root.host, label: checkoutLabel(root), branchLabel: branchLabel(root), isMain: root.checkout?.isMain ?? true, dirty: root.checkout?.dirty ?? false, families: [family]))
                }
            }
            let labels = checkoutLabels(project.checkouts.compactMap { $0.families.first?.root })
            for index in project.checkouts.indices where index < labels.count { project.checkouts[index].label = labels[index] }
            projects.append(project)
        }
        var counts: [SessionFilter: Int] = [:]
        for filter in SessionFilter.allCases {
            counts[filter] = (current ?? listed)?.counts[filter.viewScope]?.families
        }
        let flat = (listed?.families ?? []).compactMap { family(from: $0, rows: rows, cells: cells) }
        return SessionTree(projects: projects, needsYou: needsYou(in: current ?? listed, rows: rows, attention: attention), attention: attention, counts: counts, flat: flat)
    }

    static func family(from view: ViewFamily, rows: [String: SessionState], cells: [String: SessionCell] = [:]) -> SessionFamily? {
        func cell(_ id: String) -> SessionCell? {
            guard let row = rows[id] else { return nil }
            return cells[id] ?? SessionCell(row)
        }
        guard let root = cell(view.rootId) else { return nil }
        let members = view.members.filter { $0.id != view.rootId }.compactMap { member in
            cell(member.id).map { SessionFamily.Member(cell: $0, depth: member.depth, isWorker: member.worker == true, isTask: member.task == true) }
        }
        return SessionFamily(root: root, members: members, workingSubagents: view.workingSubagents, workers: view.workers, tasks: view.tasks)
    }

    static func needsYou(in view: ScopePage.View?, rows: [String: SessionState], attention: [String: SessionAttention]) -> [SessionState] {
        var seen: Set<String> = []
        return (view?.families ?? []).flatMap(\.members).compactMap { member in
            guard let kind = attention[member.id]?.kind, kind != .replacedRequest, seen.insert(member.id).inserted else { return nil }
            return rows[member.id]
        }
    }

    private static func checkoutID(of root: SessionState, view: ViewFamily?) -> String {
        if let id = view?.checkoutId, !id.isEmpty { return id }
        return (root.checkout?.path ?? root.cwd ?? root.project?.id ?? noProjectID) + (root.remoteHostID.map { "@" + $0 } ?? "")
    }
}
