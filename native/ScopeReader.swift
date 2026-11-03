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
    private var reads: [ScopeRequest: Task<Void, Never>] = [:]
    private var searchGeneration: UInt64 = 0
    private var rereadTask: Task<Void, Never>?
    private let source: Source

    init(_ source: Source) { self.source = source }

    func cancel() {
        rereadTask?.cancel()
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
            if let view = page.view { views[request] = view }
            if pages.append(page, to: request, after: read) { adoptCurrent() }
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
            if !pages.found.isEmpty {
                pages.setFound([])
                source.changed()
            }
            return
        }
        var searched = request
        searched.query = query
        let page = try? await api.scopePage(searched, timeout: source.timeout())
        guard source.active(), !Task.isCancelled, generation == searchGeneration else { return }
        pages.setFound(page?.sessions ?? [])
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

    func rereadAll() {
        rereadTask?.cancel()
        rereadTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(300))
            guard let self, !Task.isCancelled else { return }
            for request in pages.listings.keys.sorted() { await reread(request) }
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
                view = view ?? page.view
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
