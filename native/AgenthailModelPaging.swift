import Foundation

extension AgenthailModel {
    var listRequest: ScopeRequest? {
        let refinement = sessionRefinement
        guard refinement.isActive || sessionFilter.pagedScope != nil else { return nil }
        return ScopeRequest(scope: sessionFilter.viewScope, surface: refinement.surface, status: refinement.status?.rawValue)
    }

    var listHasMore: Bool { listRequest.map { pages.hasMore($0) } ?? false }

    var filterCounts: [SessionFilter: Int] {
        let local = sessionTree.counts
        if sessionRefinement.isActive {
            guard let view = listRequest.flatMap({ reader.views[$0] }) else { return [:] }
            return Dictionary(uniqueKeysWithValues: SessionFilter.allCases.compactMap { filter in view.counts[filter.viewScope].map { (filter, $0.families) } })
        }
        return Dictionary(uniqueKeysWithValues: SessionFilter.allCases.map { filter in
            (filter, filter.pagedScope.flatMap { currentView?.counts[$0]?.families } ?? local[filter] ?? 0)
        })
    }

    func paletteSessions(_ query: String) -> [SessionState] {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let found = Set(pages.found.map(\.id))
        return knownSessions.filter { session in
            trimmed.isEmpty || found.contains(session.id) || [session.title, session.name, session.alias, session.project?.displayName, session.checkout?.branch]
                .compactMap { $0 }
                .contains { $0.localizedCaseInsensitiveContains(trimmed) }
        }
    }

    var refinementSurfaces: [String] {
        let view = listRequest.flatMap { reader.views[$0] } ?? currentView
        let listed = view?.surfaces.map(\.surface) ?? []
        return Array(Set(listed + (snapshot?.surfaces ?? []).map(\.name))).sorted()
    }

    func earlierRequest(_ projectID: String) -> ScopeRequest { ScopeRequest(scope: "all", projectID: projectID) }

    func listEarlier(_ projectID: String) async { await reader.readMore(earlierRequest(projectID)) }

    func hasEarlier(_ projectID: String) -> Bool { pages.hasMore(earlierRequest(projectID)) }

    func listMore() async {
        guard let request = listRequest else { return }
        await reader.readMore(request)
    }

    func listChanged() {
        guard let request = listRequest else { return }
        reader.requests.filter { $0 != request && $0.projectID == nil }.forEach(reader.drop)
        if pages.listings[request] == nil { Task { await reader.readMore(request) } }
        scheduleListReread()
    }

    func scheduleListReread() {
        listRereadTask?.cancel()
        guard let request = listRequest, let raw = reader.views[request]?.refreshAt, let at = TimestampParser.date(raw) else { return }
        listRereadTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(max(1, at.timeIntervalSinceNow)))
            guard !Task.isCancelled else { return }
            self?.reader.rereadAll()
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

    func makeReader() -> ScopeReader {
        ScopeReader(ScopeReader.Source(
            api: { [weak self] in self?.api },
            current: { [weak self] in self?.snapshot?.sessions ?? [] },
            active: { true },
            reachable: { [weak self] in self?.snapshot != nil && self?.connectionError == nil },
            changed: { [weak self] in
                guard let self else { return }
                objectWillChange.send()
                rederive()
                scheduleListReread()
            }
        ))
    }
}
