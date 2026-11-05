@testable import AgenthailHelpers
import Foundation

final class PagingDaemon: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requests: [String] = []
    nonisolated(unsafe) static var epoch = "e1"
    nonisolated(unsafe) static var archived: [String] = ["shelved"]
    nonisolated(unsafe) static var slowPages: Set<String> = []
    nonisolated(unsafe) static var firstAll = ["live", "older"]
    nonisolated(unsafe) static var servedFirstAll = 0
    nonisolated(unsafe) static var baseEpochs: [String] = []
    static let lock = NSLock()

    static func row(_ id: String, status: String = "idle", current: Bool = false, name: String? = nil) -> String {
        #"{"id":"\#(id)","surface":"claude","name":"\#(name ?? id)","status":"\#(status)","lastActive":"2026-11-03T11:00:00Z","queueCount":0,"open":true,"current":\#(current),"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}}"#
    }

    static func order(_ ids: [String]) -> String {
        let families = ids.map { #"{"id":"\#($0)","rootId":"\#($0)","projectId":"","checkoutId":"","members":[{"id":"\#($0)","depth":0,"contextOnly":false,"pinned":false}],"pinned":false,"workingSubagents":0}"# }
        let collapsed = ids.map { #""\#($0)""# }.joined(separator: ",")
        return #""projects":[{"id":"","name":"No project","counts":{"families":\#(ids.count),"sessions":\#(ids.count)},"collapsedFamilyIds":[\#(collapsed)]}],"families":[\#(families.joined(separator: ","))]"#
    }

    static func page(_ ids: [String], next: String?, current: Bool = false, refreshAt: String? = nil) -> String {
        let view = #"{"counts":{"running":{"families":1,"sessions":1},"current":{"families":2,"sessions":2},"recent":{"families":3,"sessions":3},"all":{"families":5,"sessions":5},"archived":{"families":1,"sessions":1}},"surfaces":[{"surface":"claude","counts":{}},{"surface":"codex","counts":{}}],\#(order(ids))\#(refreshAt.map { #","refreshAt":"\#($0)""# } ?? "")}"#
        return #"{"sessions":[\#(ids.map { row($0, current: current) }.joined(separator: ","))],"hostEpoch":"\#(epoch)","catalogSeq":5,"view":\#(view)\#(next.map { #","nextCursor":"\#($0)""# } ?? "")}"#
    }

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "paging.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        let query = Dictionary(uniqueKeysWithValues: (URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") })
        Self.lock.withLock { Self.requests.append(url.path + "?" + (url.query ?? "")) }
        let body: String
        switch (url.path, query["scope"], query["cursor"], query["q"]) {
        case ("/api/v1/projects/activity", _, _, _): body = #"{"projects":[]}"#
        case ("/api/v1/history", _, _, _): body = #"{"items":[],"hasMore":false,"nextBefore":0,"kinds":[]}"#
        case ("/api/v1/snapshot", nil, _, _):
            let epoch = Self.lock.withLock { Self.baseEpochs.isEmpty ? Self.epoch : Self.baseEpochs.removeFirst() }
            body = #"{"updatedAt":"2026-11-03T12:00:00Z","hostEpoch":"\#(epoch)","catalogSeq":5,"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"queue":[],"channels":[],"relays":[],"attention":[],"codexRecentHours":5}"#
        case ("/api/v1/snapshot", "current", _, _): body = Self.page(["live", "lead"], next: nil, current: true)
        case ("/api/v1/snapshot", "all", _, let q?) where !q.isEmpty: body = Self.page(q == "zet" || q == "gps" ? ["zeta"] : q == "ze" ? ["live", "zeta"] : [], next: nil)
        case ("/api/v1/snapshot", "all", nil, _): body = Self.page(Self.firstAll, next: "p2", refreshAt: query["status"] == nil ? nil : "2026-11-03T12:00:00Z")
        case ("/api/v1/snapshot", "all", "p2", _): body = Self.page(["oldest", "zeta"], next: nil)
        case ("/api/v1/snapshot", "archived", _, _): body = Self.page(Self.archived, next: nil)
        default: body = "{}"
        }
        if query["q"] == "slow" { Thread.sleep(forTimeInterval: 0.3) }
        let plainAll = query["scope"] == "all" && query["q"] == nil && query["status"] == nil
        if plainAll && Self.slowPages.contains(query["cursor"] ?? "first") { Thread.sleep(forTimeInterval: 0.3) }
        finish(url, body)
        if plainAll && query["cursor"] == nil { Self.lock.withLock { Self.servedFirstAll += 1 } }
    }

    private func finish(_ url: URL, _ body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct MacPagingTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    @MainActor
    static func settle(_ condition: () -> Bool) async -> Bool {
        let deadline = Date().addingTimeInterval(3)
        while !condition() && Date() < deadline { try? await Task.sleep(for: .milliseconds(10)) }
        return condition()
    }

    @MainActor
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [PagingDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://paging.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let ids = { Set(model.knownSessions.map(\.id)) }

        check(await model.refresh(), "the Mac reads base, current, activity and history")
        check(ids() == ["live", "lead"], "only the current sessions are held at first: \(ids())")
        check(!PagingDaemon.requests.contains { $0 == "/api/v1/snapshot?" }, "the Mac never asks for the unpaged snapshot: \(PagingDaemon.requests)")
        check(model.filterCounts[.all] == 5 && model.filterCounts[.archived] == 1, "paged tabs count by the daemon's view: \(model.filterCounts)")
        check(model.filterCounts[.recent] == model.sessionTree.counts[.recent], "Recent counts the list it shows: \(model.filterCounts)")

        model.sessionFilter = .all
        check(await settle { ids().contains("older") }, "All reads its first page")
        check(model.listHasMore, "All knows another page waits")
        PagingDaemon.slowPages = ["p2"]
        let shown = Task { await model.listMore() }
        try? await Task.sleep(for: .milliseconds(50))
        shown.cancel()
        await model.listMore()
        PagingDaemon.slowPages = []
        check(ids().isSuperset(of: ["oldest", "zeta"]) && !model.listHasMore, "a page keeps loading when the row that asked for it goes away: \(ids())")

        model.sessionRefinement = SessionRefinement(status: .working, surface: "claude")
        check(await settle { PagingDaemon.requests.contains { $0.contains("scope=all") && $0.contains("status=working") && $0.contains("surface=claude") } }, "a refinement goes to the daemon: \(PagingDaemon.requests.suffix(3))")
        check(await settle { model.sessionTree.projects.flatMap { $0.checkouts.flatMap(\.families) }.map(\.id).sorted() == ["live", "older"] }, "a refined list shows the daemon's rows, not a local filter")
        check(model.refinementSurfaces.contains("codex"), "surface choices come from the daemon's view")
        check(model.filterCounts[.recent] == 2 && model.filterCounts[.running] == 1, "a refined list counts every tab by the daemon: \(model.filterCounts)")
        check(model.pages.listings.keys.filter { $0.projectID == nil }.count == 1, "only the shown list is kept")
        model.sessionRefinement = SessionRefinement()
        model.sessionFilter = .recent

        model.findSessions("zet")
        check(await settle { model.pages.found.map(\.id) == ["zeta"] }, "search asks the daemon")
        check(await model.mentionSessions("zet").map(\.id) == ["zeta"], "mentions ask the daemon")
        model.findSessions("gps")
        check(await settle { model.pages.found.map(\.id) == ["zeta"] }, "the palette's query goes to the daemon")
        check(model.paletteSessions("gps").map(\.id) == ["zeta"], "a daemon match the title does not show stays in the palette")
        model.findSessions("ze")
        check(await settle { model.pages.found.map(\.id) == ["live", "zeta"] }, "a search lists the daemon's rows")
        check(model.paletteSessions("ze").map(\.id) == ["live", "zeta"], "the palette keeps the daemon's order, title matches included: \(model.paletteSessions("ze").map(\.id))")
        let abandoned = Task { await model.reader.search("slow", in: ScopeRequest(scope: "all")) }
        try? await Task.sleep(for: .milliseconds(50))
        abandoned.cancel()
        await abandoned.value
        check(model.pages.found.isEmpty && model.reader.foundView == nil && !model.reader.searchPending("slow"), "a cancelled search leaves no earlier query's rows: \(model.pages.found.map(\.id))")
        model.findSessions("zet")
        check(await settle { model.pages.found.map(\.id) == ["zeta"] }, "a search after a cancelled one lands")
        model.mainPane.select("zeta")
        model.findSessions("")
        check(await settle { model.pages.found.isEmpty }, "clearing the search drops what it found")
        check(model.knownSessions.contains { $0.id == "zeta" }, "a session opened from search stays open after the search clears")

        model.sessionFilter = .archived
        PagingDaemon.firstAll = ["left"]
        PagingDaemon.slowPages = ["first"]
        let asked = PagingDaemon.lock.withLock { PagingDaemon.requests.count }
        model.sessionFilter = .all
        try? await Task.sleep(for: .milliseconds(50))
        PagingDaemon.firstAll = ["returned"]
        model.sessionFilter = .archived
        model.sessionFilter = .all
        check(await settle { ids().contains("returned") }, "a list left while its first page was in flight reads its own first page when it comes back: \(ids())")
        try? await Task.sleep(for: .milliseconds(400))
        let firstPages = PagingDaemon.lock.withLock { PagingDaemon.requests.dropFirst(asked).filter { $0.contains("scope=all") && !$0.contains("cursor") && !$0.contains("q=") } }
        check(!ids().contains("left") && firstPages.count == 2, "coming back does not wait on the read it left: \(ids()) \(firstPages)")
        PagingDaemon.slowPages = []

        PagingDaemon.firstAll = ["stale"]
        model.sessionFilter = .archived
        let served = PagingDaemon.lock.withLock { PagingDaemon.servedFirstAll }
        model.sessionFilter = .all
        try? await Task.sleep(for: .milliseconds(1))
        let deadline = Date().addingTimeInterval(3)
        while PagingDaemon.lock.withLock({ PagingDaemon.servedFirstAll }) == served && Date() < deadline { usleep(5_000) }
        check(Date() < deadline, "All asked for its first page")
        usleep(50_000)
        PagingDaemon.firstAll = ["fresh"]
        model.sessionFilter = .archived
        model.sessionFilter = .all
        check(await settle { ids().contains("fresh") }, "All read again after leaving and returning reads its own first page: \(ids()) \(PagingDaemon.requests.suffix(8)) \(model.pages.listings.mapValues { $0.rows.map(\.id) })")
        try? await Task.sleep(for: .milliseconds(100))
        check(!ids().contains("stale") && model.listHasMore, "a page that arrived for a list that was left does not land in the list read again: \(ids())")
        PagingDaemon.firstAll = ["live", "older"]
        model.sessionFilter = .recent

        var published: [[SpotlightEntry]] = []
        model.indexSpotlight(priority: .userInitiated) { published.append($0) }
        _ = await settle { !published.isEmpty || model.spotlightCatalog?.walking != true }
        check(!published.isEmpty, "Spotlight is walked once: walking \(model.spotlightCatalog?.walking == true), epoch \(model.spotlightCatalog?.walkedEpoch ?? "none"), \(PagingDaemon.lock.withLock { PagingDaemon.requests.suffix(6) })")
        check(Set(published.last!.map(\.id)) == ["live", "older", "oldest", "zeta", "shelved"], "the walk covers every session, archived too: \(published.last!.map(\.id))")
        let walks = PagingDaemon.requests.filter { $0.contains("scope=archived") }.count
        _ = await model.refresh()
        try? await Task.sleep(for: .milliseconds(100))
        check(PagingDaemon.requests.filter { $0.contains("scope=archived") }.count == walks, "a refresh in the same epoch does not walk again")
        model.spotlightCatalog?.apply(upserted: [], removed: ["oldest"])
        check(!published.last!.contains { $0.id == "oldest" }, "a removal from the catalog leaves Spotlight")
        PagingDaemon.archived = ["shelved", "boxed"]
        PagingDaemon.epoch = "e2"
        _ = await model.refresh()
        check(await settle { published.last!.contains { $0.id == "boxed" } }, "a new epoch walks again")
        PagingDaemon.lock.withLock { PagingDaemon.baseEpochs = ["e2"] }
        PagingDaemon.epoch = "e3"
        check(await model.refresh(), "a refresh that spans a restart reads both parts again")
        check(model.catalogPosition.epoch == "e3" && model.currentViewPosition.epoch == "e3" && model.currentView != nil, "the restarted daemon's Current view is installed: \(model.catalogPosition.epoch ?? "none") \(model.currentViewPosition.epoch ?? "none")")
        model.stopIndexingSpotlight()
        print("ok   mac_paging_test")
    }
}
