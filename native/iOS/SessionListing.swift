import Foundation
import Observation

@MainActor
protocol ListingRouting: AnyObject {
    var listedHosts: [HostConnection] { get }
    var snapshot: DashboardSnapshot? { get }
    var sessionAttention: [String: SessionAttention] { get }
    var cells: SessionCells { get }
    var projects: [String: ListedProject] { get }
    func listedHost(_ hostID: String) -> HostConnection?
}

@MainActor
@Observable
final class SessionListing {
    @ObservationIgnored private var listedTree: (key: ListKey, list: SessionList)?
    var scope = SessionFilter.recent {
        didSet { readListing() }
    }
    var refinement = SessionRefinement() {
        didSet { readListing() }
    }
    private(set) var listRevision: UInt64 = 0
    @ObservationIgnored private var orderHold = OrderHold()
    @ObservationIgnored private var heldList: ScopePage.View?
    @ObservationIgnored private var heldKey: ListKey?
    @ObservationIgnored private var shownOrder: [ScopePage.View?] = []
    private(set) var listing = ListingState()

    @ObservationIgnored private weak var router: ListingRouting?
    @ObservationIgnored private let emptyCells = SessionCells()

    init(router: ListingRouting) {
        self.router = router
    }

    private var listedHosts: [HostConnection] { router?.listedHosts ?? [] }
    private var snapshot: DashboardSnapshot? { router?.snapshot }
    private var sessionAttention: [String: SessionAttention] { router?.sessionAttention ?? [:] }
    private var cells: SessionCells { router?.cells ?? emptyCells }
    private var projects: [String: ListedProject] { router?.projects ?? [:] }

    func rederive() { rederiveTree() }

    func read() { readListing() }

    func hostsChanged() {
        let order = orderSignature
        if order != shownOrder {
            shownOrder = order
            rederiveTree()
        }
    }

    nonisolated static func listRequest(_ scope: SessionFilter, _ refinement: SessionRefinement) -> ScopeRequest? {
        guard refinement.isActive || scope != .recent else { return nil }
        return ScopeRequest(scope: scope.viewScope, surface: refinement.surface, status: refinement.status?.rawValue)
    }

    var listRequest: ScopeRequest? { Self.listRequest(scope, refinement) }

    private func readListing() {
        listing = ListingState.read(listedHosts, request: listRequest, projects: projects, earlier: earlierTargets)
    }

    func listMore() async {
        await SessionScopes.readMore(SessionScopes.targets(listRequest, on: listedHosts))
    }

    var canListMore: Bool { listing.more }

    private func earlierTargets(_ project: ListedProject) -> [ScopeTarget] {
        let request = ScopeRequest(scope: "all", projectID: project.state.id)
        let shared = ProjectKey.names(project.state.id)
        let direct = shared ? project.servers + project.state.checkouts.map { ProjectRouting.owner($0.host, of: project) } : []
        var seen: Set<String> = []
        return ([project.hostID] + direct).compactMap { hostID in
            guard seen.insert(hostID).inserted, let host = router?.listedHost(hostID) else { return nil }
            return ScopeTarget(host: host, request: request)
        }
    }

    func listEarlier(_ project: ListedProject) async { await SessionScopes.readMore(earlierTargets(project)) }

    func hasEarlier(_ project: ListedProject) -> Bool { listing.earlier.contains(ProjectContents.key(of: project)) }

    func earlierPlace(_ project: ListedProject) -> [String] {
        earlierTargets(project).map { "\($0.host.pages.listings[$0.request]?.cursor ?? "")|\($0.host.pages.hasMore($0.request))" }
    }

    var listingError: String? { listing.error }

    func searchScope(_ query: String) async {
        let request = ScopeRequest(scope: scope.viewScope, surface: refinement.surface, status: refinement.status?.rawValue)
        await SessionScopes.search(query.trimmingCharacters(in: .whitespacesAndNewlines), in: request, on: listedHosts)
    }

    struct ListKey: Equatable {
        let scope: SessionFilter
        let refinement: SessionRefinement
        let search: String

        var request: ScopeRequest? { SessionListing.listRequest(scope, refinement) }
    }

    struct SessionList: Equatable {
        var tree: SessionTree
        var counts: [SessionFilter: Int]
        var surfaces: [String]
    }

    // Families hold session cells, so a change inside one session leaves the tree equal.
    func sessionTree(search: String) -> SessionTree { sessionList(search: search).tree }

    func sessionList(search: String) -> SessionList {
        _ = listRevision
        let key = ListKey(scope: scope, refinement: refinement, search: search)
        if let listed = listedTree, listed.key == key { return listed.list }
        let list = buildList(key)
        listedTree = (key, list)
        return list
    }

    private func rederiveTree() {
        let key = listedTree?.key ?? ListKey(scope: scope, refinement: refinement, search: "")
        let list = buildList(key)
        if let listed = listedTree, listed.list == list { return }
        listedTree = (key, list)
        listRevision &+= 1
    }

    private func buildList(_ key: ListKey) -> SessionList {
        if orderHold.held, heldKey != key { orderHold.release() }
        let rows = Dictionary((snapshot?.sessions ?? []).map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let shown = orderHold.shown(list: listOrder(key), current: currentOrder, rows: rows)
        heldList = shown.list
        let tree = SessionTree.ordered(list: shown.list, current: shown.current, rows: shown.rows, attention: sessionAttention, cells: orderHold.cells(live: cells.byID, rows: shown.rows))
        let counted = key.refinement.isActive ? key.request.flatMap { request in joinedOrder { $0.view(request) } } : currentOrder
        let counts = Dictionary(uniqueKeysWithValues: SessionFilter.allCases.map { ($0, counted?.counts[$0.viewScope]?.families ?? 0) })
        let listed = (counted ?? currentOrder)?.surfaces.map(\.surface) ?? []
        let configured = listedHosts.flatMap { $0.snapshot?.surfaces.map(\.name) ?? [] }
        let surfaces = Set(listed + configured + [key.refinement.surface].compactMap { $0 }).sorted()
        return SessionList(tree: tree, counts: counts, surfaces: surfaces)
    }

    private var orderHosts: [HostConnection] { listedHosts.filter(\.shownReachable) + listedHosts.filter { !$0.shownReachable } }

    private func joinedOrder(_ pick: (HostConnection) -> ScopePage.View?) -> ScopePage.View? {
        ScopePage.View.joined(orderHosts.compactMap { host in pick(host)?.renamed { HostCatalog.qualify(host.id, $0) } })
    }

    var currentOrder: ScopePage.View? { joinedOrder(\.currentView) }

    /// Sessions in the daemon's order: Current first, then each loaded listing; others keep their place after.
    func inDaemonOrder(_ sessions: [SessionState]) -> [SessionState] {
        let listings = listedHosts.flatMap { host in host.reader.requests.sorted().compactMap { host.view($0)?.renamed { HostCatalog.qualify(host.id, $0) } } }
        return DaemonOrder.sorted(sessions, by: DaemonOrder.rank([currentOrder].compactMap { $0 } + listings))
    }

    private var orderSignature: [ScopePage.View?] {
        listedHosts.flatMap { host in [host.currentView, host.reader.foundView] + host.reader.requests.sorted().map(host.view) }
    }

    func searchPending(_ search: String) -> Bool { listing.searchPending(search) }

    func listOrder(_ key: ListKey) -> ScopePage.View? {
        if !key.search.isEmpty { return joinedOrder { $0.reader.foundView } }
        guard let request = key.request else { return currentOrder }
        return joinedOrder { $0.view(request) }
    }

    /// Keeps the list's order while a finger is on it; rows still update in place.
    func holdOrder(_ holding: Bool) {
        guard holding != orderHold.held else { return }
        if holding {
            let rows = Dictionary((snapshot?.sessions ?? []).map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
            orderHold.hold(list: heldList, current: currentOrder, rows: rows)
            heldKey = listedTree?.key
        } else {
            orderHold.release()
            rederiveTree()
        }
    }
}
