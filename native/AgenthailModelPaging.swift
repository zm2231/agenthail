import Foundation

extension AgenthailModel {
    var listRequest: ScopeRequest? {
        let refinement = sessionRefinement
        guard refinement.isActive || sessionFilter != .recent else { return nil }
        return ScopeRequest(scope: sessionFilter.viewScope, surface: refinement.surface, status: refinement.status?.rawValue)
    }

    var listView: ScopePage.View? { listRequest.map { reader.views[$0] } ?? currentView }

    var listHasMore: Bool { listRequest.map { pages.hasMore($0) } ?? false }

    var filterCounts: [SessionFilter: Int] {
        let view = sessionRefinement.isActive ? listRequest.flatMap { reader.views[$0] } : currentView
        guard let view else { return [:] }
        return Dictionary(uniqueKeysWithValues: SessionFilter.allCases.compactMap { filter in view.counts[filter.viewScope].map { (filter, $0.families) } })
    }

    /// Each known session's place in the daemon's order: Current first, then the loaded listings.
    var daemonOrder: [String: Int] {
        var order: [String: Int] = [:]
        let views = [currentView] + reader.requests.sorted().map { reader.views[$0] }
        for member in views.compactMap({ $0 }).flatMap({ $0.families.flatMap(\.members) }) where order[member.id] == nil {
            order[member.id] = order.count
        }
        return order
    }

    func inDaemonOrder(_ sessions: [SessionState]) -> [SessionState] {
        let order = daemonOrder
        return sessions.enumerated().sorted { lhs, rhs in
            let (left, right) = (order[lhs.element.id] ?? Int.max, order[rhs.element.id] ?? Int.max)
            return left != right ? left < right : lhs.offset < rhs.offset
        }.map(\.element)
    }

    func paletteSessions(_ query: String) -> [SessionState] {
        query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? inDaemonOrder(knownSessions) : pages.found
    }

    var refinementSurfaces: [String] {
        let view = listRequest.flatMap { reader.views[$0] } ?? currentView
        let listed = view?.surfaces.map(\.surface) ?? []
        return Array(Set(listed + surfaceNames)).sorted()
    }

    func earlierRequest(_ projectID: String) -> ScopeRequest { ScopeRequest(scope: "all", projectID: projectID) }

    func listEarlier(_ projectID: String) async { await reader.readMore(earlierRequest(projectID)) }

    func hasEarlier(_ projectID: String) -> Bool { pages.hasMore(earlierRequest(projectID)) }

    func scopeRequest(_ projectID: String) -> ScopeRequest {
        let archived = projects.first { $0.id == projectID }?.archived == true
        let refinement = sessionRefinement
        return ScopeRequest(scope: archived ? SessionFilter.archived.viewScope : sessionFilter.viewScope, projectID: projectID, surface: refinement.surface, status: refinement.status?.rawValue)
    }

    func scopeTree(_ projectID: String) -> SessionTree { scopedTrees[scopeRequest(projectID)] ?? .empty }

    func buildScopedTrees() -> [ScopeRequest: SessionTree] {
        let archived = SessionFilter.archived.viewScope
        return reader.views.reduce(into: [:]) { trees, entry in
            guard entry.key.projectID != nil || entry.key.scope == archived else { return }
            let ids = Set(entry.value.families.flatMap { $0.members.map(\.id) })
            let rows = Dictionary(uniqueKeysWithValues: ids.compactMap { id in cells.session(id).map { (id, $0) } })
            trees[entry.key] = scopeTrees.tree(for: entry.key, view: entry.value, rows: rows, attention: sessionAttention.filter { ids.contains($0.key) }, cells: cells.byID, keeping: reader.requests)
        }
    }

    func scopeHasMore(_ projectID: String) -> Bool { pages.hasMore(scopeRequest(projectID)) }

    func readScope(_ projectID: String) async {
        dropUnusedScopes()
        await reader.readMore(scopeRequest(projectID))
    }

    func listMore() async {
        guard let request = listRequest else { return }
        await reader.readMore(request)
    }

    var archivedRequest: ScopeRequest? {
        SessionListLayout.archivedRequest(filter: sessionFilter, refinement: sessionRefinement, shows: showsArchived)
    }

    func archivedTree() -> SessionTree { archivedRequest.flatMap { scopedTrees[$0] } ?? .empty }

    var archivedHasMore: Bool { archivedRequest.map { pages.hasMore($0) || pages.listings[$0] == nil } ?? false }

    func readArchived() async {
        guard let request = archivedRequest else { return }
        await reader.readMore(request)
    }

    func listChanged() {
        if archivedRequest == nil {
            reader.requests.filter { $0.scope == SessionFilter.archived.viewScope && $0.projectID == nil && $0 != listRequest }.forEach(reader.drop)
        }
        guard let request = listRequest else { return }
        reader.requests.filter { $0 != request && $0 != archivedRequest && $0.projectID == nil }.forEach(reader.drop)
        if pages.listings[request] == nil { Task { await reader.readMore(request) } }
        scheduleListReread()
    }

    func scheduleListReread() {
        listRereadTask?.cancel()
        let views = [currentView, listRequest.flatMap { reader.views[$0] }]
        guard let at = views.compactMap({ $0?.refreshAt.flatMap(TimestampParser.date) }).min() else { return }
        listRereadTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(max(1, at.timeIntervalSinceNow)))
            guard !Task.isCancelled else { return }
            self?.rereadOrder()
        }
    }

    func findSessions(_ query: String) {
        findTask?.cancel()
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        findTask = Task { [weak self] in
            if !trimmed.isEmpty { try? await Task.sleep(for: .milliseconds(250)) }
            guard !Task.isCancelled, let self else { return }
            await reader.search(trimmed, in: ScopeRequest(scope: "all"))
        }
    }

    func indexSpotlight(priority: TaskPriority = .utility, _ publish: @escaping ([SpotlightEntry]) -> Void) {
        guard spotlightCatalog == nil else { return }
        let catalog = SpotlightCatalog(priority: priority, api: { [weak self] in self?.api }, publish: publish)
        spotlightCatalog = catalog
        catalog.epoch(snapshot?.hostEpoch)
    }

    func stopIndexingSpotlight() {
        spotlightCatalog?.stop()
        spotlightCatalog = nil
    }

    func mentionSessions(_ query: String) async -> [SessionState] {
        (try? await reader.mentions(query)) ?? []
    }

    func makeReader() -> ObservedScope {
        ObservedScope(ScopeReader.Source(
            api: { [weak self] in self?.api },
            current: { [weak self] in self?.snapshot?.sessions ?? [] },
            active: { true },
            reachable: { [weak self] in self?.snapshot != nil && self?.connectionError == nil },
            changed: { [weak self] in
                guard let self else { return }
                rederive()
                scheduleListReread()
            }
        ))
    }
}
