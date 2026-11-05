import Foundation

@MainActor
final class ScopeReader {
    struct Source {
        let api: () -> AgenthailAPI?
        let current: () -> [SessionState]
        let active: () -> Bool
        let reachable: () -> Bool
        let changed: () -> Void
        var timeout: () -> TimeInterval = { 25 }
    }

    private(set) var pages = ScopePages()
    private(set) var pageError: String?
    private(set) var views: [ScopeRequest: ScopePage.View] = [:]
    private(set) var foundView: ScopePage.View?
    private(set) var foundQuery: String?

    func searchPending(_ query: String) -> Bool { !query.isEmpty && foundQuery != query }
    private var reads: [ScopeRequest: Task<Void, Never>] = [:]
    private var searchGeneration: UInt64 = 0
    private var rereadTask: Task<Void, Never>?
    private var rereadDirty = false
    private let source: Source

    init(_ source: Source) { self.source = source }

    func cancel() {
        rereadTask?.cancel()
        rereadTask = nil
        rereadDirty = false
        reads.values.forEach { $0.cancel() }
        reads = [:]
    }

    var requests: Set<ScopeRequest> { Set(pages.listings.keys).union(reads.keys) }

    func drop(_ request: ScopeRequest) {
        reads.removeValue(forKey: request)?.cancel()
        pages.drop(request)
        views.removeValue(forKey: request)
    }

    func adoptCurrent() { pages.adopt(source.current()) }

    func joined(_ sessions: [SessionState]) -> [SessionState] { pages.joined(sessions) }

    func readMore(_ request: ScopeRequest) async {
        if let running = reads[request] { return await running.value }
        guard source.active(), source.reachable(), let read = pages.next(request), let api = source.api() else { return }
        let task = Task { await fetch(request, read: read, api: api) }
        reads[request] = task
        await task.value
        if reads[request] == task { reads[request] = nil }
    }

    private func fetch(_ request: ScopeRequest, read: ScopePages.Read, api: AgenthailAPI) async {
        do {
            let page = try await api.scopePage(request, cursor: read.cursor, timeout: source.timeout())
            guard source.active(), !Task.isCancelled else { return }
            pageError = nil
            if pages.append(page, to: request, after: read) {
                views[request] = read.cursor == nil ? page.view : views[request]?.merging(page.view) ?? page.view
                adoptCurrent()
            }
        } catch {
            guard source.active(), !Task.isCancelled else { return }
            pageError = error.localizedDescription
        }
        source.changed()
    }

    func search(_ query: String, in request: ScopeRequest) async {
        searchGeneration += 1
        let generation = searchGeneration
        guard !query.isEmpty, source.reachable(), let api = source.api() else {
            if !pages.found.isEmpty || foundView != nil || foundQuery != nil {
                pages.setFound([])
                foundView = nil
                foundQuery = nil
                source.changed()
            }
            return
        }
        var searched = request
        searched.query = query
        let page = try? await api.scopePage(searched, timeout: source.timeout())
        guard generation == searchGeneration else { return }
        let answered = source.active() && !Task.isCancelled ? page : nil
        pages.setFound(answered?.sessions ?? [])
        foundView = answered?.view
        foundQuery = query
        source.changed()
    }

    func mentions(_ query: String) async throws -> [SessionState] {
        guard let api = source.api() else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        var request = ScopeRequest(scope: "all")
        request.query = query
        return try await api.scopePage(request, limit: 20, timeout: source.timeout()).sessions
    }

    func refreshed(removedFromCurrent: Bool) {
        adoptCurrent()
        if removedFromCurrent, !pages.listings.isEmpty { rereadAll() }
    }

    func removed(_ id: String, unlisted: Bool) -> Bool {
        pages.remove(id) || unlisted && !pages.listings.isEmpty
    }

    /// One reread in flight; news that arrives meanwhile runs one more after it.
    func rereadAll() {
        guard rereadTask == nil else {
            rereadDirty = true
            return
        }
        rereadTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(300))
            guard let self, !Task.isCancelled else { return }
            for request in pages.listings.keys.sorted() { await reread(request) }
            rereadTask = nil
            if rereadDirty {
                rereadDirty = false
                rereadAll()
            }
        }
    }

    private func reread(_ request: ScopeRequest) async {
        guard source.active(), let api = source.api(), let start = pages.restart(request) else { return }
        var rows: [SessionState] = []
        var cursor: String?
        var view: ScopePage.View?
        do {
            repeat {
                let page = try await api.scopePage(request, cursor: cursor, limit: 200, timeout: source.timeout())
                rows += page.sessions
                cursor = page.nextCursor
                view = view?.merging(page.view) ?? page.view
            } while cursor != nil && rows.count < start.rows
        } catch {
            return
        }
        guard source.active(), !Task.isCancelled, pages.replace(request, rows: rows, cursor: cursor, generation: start.generation) else { return }
        if let view { views[request] = view }
        adoptCurrent()
        source.changed()
    }
}
