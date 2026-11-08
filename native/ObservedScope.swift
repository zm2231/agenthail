import Observation

// ScopeReader is shared with iOS and not observable; every change to it goes through here so views see it.
@MainActor
@Observable
final class ObservedScope {
    @ObservationIgnored private var reader: ScopeReader!
    private var revision = 0

    init(_ source: ScopeReader.Source) {
        let relay = ChangeRelay()
        reader = ScopeReader(ScopeReader.Source(
            api: source.api,
            current: source.current,
            active: source.active,
            reachable: source.reachable,
            changed: { relay.fire() },
            timeout: source.timeout
        ))
        relay.target = { [weak self] in
            self?.revision &+= 1
            source.changed()
        }
    }

    private var observed: ScopeReader {
        _ = revision
        return reader
    }

    var pages: ScopePages { observed.pages }
    var pageError: String? { observed.pageError }
    var pageErrors: [ScopeRequest: String] { observed.pageErrors }
    var views: [ScopeRequest: ScopePage.View] { observed.views }
    var requests: Set<ScopeRequest> { observed.requests }
    var foundView: ScopePage.View? { observed.foundView }
    func searchPending(_ query: String) -> Bool { observed.searchPending(query) }
    func joined(_ sessions: [SessionState]) -> [SessionState] { observed.joined(sessions) }
    func mentions(_ query: String) async throws -> [SessionState] { try await reader.mentions(query) }

    func drop(_ request: ScopeRequest) {
        reader.drop(request)
        revision &+= 1
    }

    func adoptCurrent() {
        reader.adoptCurrent()
        revision &+= 1
    }

    func refreshed(removedFromCurrent: Bool) {
        reader.refreshed(removedFromCurrent: removedFromCurrent)
        revision &+= 1
    }

    func removed(_ id: String, unlisted: Bool) -> Bool {
        let changed = reader.removed(id, unlisted: unlisted)
        revision &+= 1
        return changed
    }

    func readMore(_ request: ScopeRequest) async { await reader.readMore(request) }
    func search(_ query: String, in request: ScopeRequest) async { await reader.search(query, in: request) }
    func rereadAll() { reader.rereadAll() }
}

@MainActor
private final class ChangeRelay {
    var target: () -> Void = {}
    func fire() { target() }
}
