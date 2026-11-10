import XCTest
@testable import Agenthail

final class ProjectListingTests: XCTestCase {
    private static let repo = "repo:github.com/acme/widget"
    private static let local = SessionHost(id: "self", name: "self", state: "local")

    private static func checkout(_ host: SessionHost, _ projectID: String, _ root: String, added: Bool = true, archived: Bool? = nil) -> ProjectState.Checkout {
        ProjectState.Checkout(host: host, projectId: projectID, rootPath: root, added: added, archived: archived)
    }

    private static func merge(_ servers: [(String, [ProjectState])], reachable: Set<String> = ["hlaptop", "hserver"]) -> (projects: [String: ListedProject], order: [String]) {
        ProjectListing.merge(servers.map { ProjectListing.Server(id: $0.0, projects: $0.1) }, name: { ["hlaptop": "laptop", "hserver": "server"][$0] }, reachable: { reachable.contains($0) })
    }

    func testOneRepositoryOnTwoComputersIsOneProjectWithBothCheckoutsAndLeads() throws {
        let laptop = ProjectState(id: Self.repo, displayName: "widget", checkouts: [Self.checkout(Self.local, Self.repo, "/Users/a/widget")],
                                  coordinators: [ProjectState.Lead(host: Self.local, name: "front", surface: "claude", sessionId: "s1")])
        let server = ProjectState(id: Self.repo, displayName: "widget", checkouts: [Self.checkout(Self.local, Self.repo, "/srv/widget")],
                                  coordinators: [ProjectState.Lead(host: Self.local, name: "back", surface: "codex", sessionId: "s2")])
        let (projects, order) = Self.merge([("hlaptop", [laptop]), ("hserver", [server])])

        XCTAssertEqual(order, [Self.repo])
        let listed = try XCTUnwrap(projects[Self.repo])
        XCTAssertEqual(listed.hostID, "hlaptop")
        XCTAssertEqual(listed.servers, ["hlaptop", "hserver"])
        XCTAssertEqual(listed.state.checkouts.map(\.rootPath), ["/Users/a/widget", "/srv/widget"])
        XCTAssertEqual(listed.state.checkouts.map(\.host.id), ["hlaptop", "hserver"])
        XCTAssertEqual(listed.state.checkouts.map(\.host.state), ["local", "connected"])
        XCTAssertEqual(listed.state.checkouts.map(\.projectId), [Self.repo, "hserver::" + Self.repo], "the second computer's checkout is named as its own")
        XCTAssertEqual(listed.state.leads.map(\.name), ["front", "back"])
        XCTAssertEqual(listed.state.leads.map { ProjectRouting.owner($0.host, of: listed) }, ["hlaptop", "hserver"])
        XCTAssertEqual(ProjectContents.leadSessionID(listed.state.leads[1], of: listed), "hserver::s2")
    }

    func testPeersListingTheSameCheckoutShowItOnceFromItsOwnComputer() throws {
        let serverHost = SessionHost(id: "hserver", name: "server", state: "connected")
        let laptop = ProjectState(id: Self.repo, displayName: "widget", checkouts: [
            Self.checkout(Self.local, Self.repo, "/Users/a/widget"),
            Self.checkout(serverHost, "hserver::" + Self.repo, "/srv/widget-mirror"),
        ])
        let server = ProjectState(id: Self.repo, displayName: "widget", checkouts: [Self.checkout(Self.local, Self.repo, "/srv/widget")])
        let listed = try XCTUnwrap(Self.merge([("hlaptop", [laptop]), ("hserver", [server])]).projects[Self.repo])
        XCTAssertEqual(listed.state.checkouts.map(\.rootPath), ["/Users/a/widget", "/srv/widget"], "the owner's own copy wins over a mirror")

        let mirrorOnly = try XCTUnwrap(Self.merge([("hlaptop", [laptop])], reachable: ["hlaptop"]).projects[Self.repo])
        XCTAssertEqual(mirrorOnly.state.checkouts.map(\.rootPath), ["/Users/a/widget", "/srv/widget-mirror"], "a mirror stands in while the owner is not paired")
        XCTAssertEqual(mirrorOnly.state.checkouts[1].host.state, "unreachable")
    }

    func testTheLeadsLineNamesEachComputerAsTheListDoes() throws {
        let vps = SessionHost(id: "hvps", name: "vps.example.ts.net", state: "connected")
        let project = ProjectState(id: Self.repo, displayName: "widget",
                                   checkouts: [Self.checkout(Self.local, Self.repo, "/w"), Self.checkout(vps, "hvps::" + Self.repo, "/srv/w")],
                                   coordinators: [ProjectState.Lead(host: Self.local, name: "front", surface: "claude"), ProjectState.Lead(host: vps, name: "back", surface: "codex")])
        let listed = try XCTUnwrap(ProjectListing.merge([ProjectListing.Server(id: "hlaptop", projects: [project])], name: { ["hlaptop": "laptop.example.ts.net"][$0] }, reachable: { _ in true }).projects[Self.repo])
        XCTAssertEqual(ProjectContents.leadsLine(listed), "@front on laptop · @back on vps", "the paired display name, never the raw host name")
        XCTAssertNil(ProjectContents.leadsLine(ListedProject(state: ProjectState(id: "p", displayName: "p", coordinators: [ProjectState.Lead(name: "only", surface: "claude")]), hostID: "hlaptop")))
    }

    func testArchivedOnlyWhenEveryComputerArchivedIt() throws {
        func project(archived: Bool) -> ProjectState {
            ProjectState(id: Self.repo, displayName: "widget", checkouts: [Self.checkout(Self.local, Self.repo, "/w", archived: archived)], archived: archived)
        }
        XCTAssertEqual(Self.merge([("hlaptop", [project(archived: true)]), ("hserver", [project(archived: false)])]).projects[Self.repo]?.state.archived, false)
        XCTAssertEqual(Self.merge([("hlaptop", [project(archived: true)]), ("hserver", [project(archived: true)])]).projects[Self.repo]?.state.archived, true)
    }

    func testTheNameComesFromTheFirstComputerThatAddedIt() throws {
        let unnamed = ProjectState(id: Self.repo, displayName: "widget", checkouts: [Self.checkout(Self.local, Self.repo, "/w", added: false)])
        let named = ProjectState(id: Self.repo, displayName: "Widget App", checkouts: [Self.checkout(Self.local, Self.repo, "/srv/w")])
        XCTAssertEqual(Self.merge([("hlaptop", [unnamed]), ("hserver", [named])]).projects[Self.repo]?.state.displayName, "Widget App")
    }

    func testFoldersStayPerComputer() {
        let folder = ProjectState(id: "folder:notes", displayName: "notes", checkouts: [Self.checkout(Self.local, "folder:notes", "/notes")])
        XCTAssertEqual(Self.merge([("hlaptop", [folder]), ("hserver", [folder])]).order, ["hlaptop::folder:notes", "hserver::folder:notes"])
    }
}

final class ProjectRoutingTests: XCTestCase {
    private static let repo = "repo:github.com/acme/widget"

    @MainActor
    private func pairedModel() throws -> AgenthailIOSModel {
        KeychainStore.removeAll()
        ProjectActionProtocol.state.reset()
        try AgenthailIOSTests.pair("hlaptop", "https://laptop.invalid", name: "laptop")
        try AgenthailIOSTests.pair("hserver", "https://server.invalid", name: "server")
        let model = AgenthailIOSModel(autoConnect: false, session: FixtureScope.open(self).session([ProjectActionProtocol.self]))
        for (hostID, root, lead) in [("hlaptop", "/Users/a/widget", "front"), ("hserver", "/srv/widget", "back")] {
            let json = #"{"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"codexRecentHours":24,"projects":[{"id":"\#(Self.repo)","displayName":"widget","checkouts":[{"host":{"id":"\#(hostID)","name":"\#(hostID)","state":"local"},"projectId":"\#(Self.repo)","rootPath":"\#(root)","added":true}],"coordinators":[{"host":{"id":"\#(hostID)","name":"\#(hostID)","state":"local"},"name":"\#(lead)","surface":"claude"}]}]}"#
            model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(json.utf8)), host: hostID)
        }
        return model
    }

    @MainActor
    func testEachProjectActionGoesToTheComputerItActsOn() async throws {
        let model = try pairedModel()
        defer { KeychainStore.removeAll() }
        let project = try XCTUnwrap(model.projects[Self.repo])
        XCTAssertEqual(project.state.checkouts.count, 2)

        let server = try XCTUnwrap(project.state.checkouts.first { $0.host.id == "hserver" })
        model.settle(await model.removeProject(server.projectId, of: project))
        XCTAssertNil(model.operationError)
        XCTAssertEqual(ProjectActionProtocol.state.take().map { "\($0.host) \($0.action) \($0.projectID)" }, ["server.invalid project-remove \(Self.repo)"], "remove reaches only the checkout's computer, by its own id")

        model.settle(await model.renameProject(project, to: "Widget"))
        XCTAssertEqual(Set(ProjectActionProtocol.state.take().map { "\($0.host) \($0.action)" }), ["laptop.invalid project-rename", "server.invalid project-rename"])

        model.settle(await model.clearLead(of: project))
        XCTAssertEqual(Set(ProjectActionProtocol.state.take().map { "\($0.host) \($0.action)" }), ["laptop.invalid coordinator-clear", "server.invalid coordinator-clear"])

        _ = await model.startLead(of: project, in: server, surface: "codex", name: "", replace: false)
        let started = ProjectActionProtocol.state.take()
        XCTAssertEqual(started.map { "\($0.host) \($0.action)" }, ["server.invalid coordinator-new"])
        XCTAssertNil(started.first?.remoteHost, "a directly paired computer is asked without a host")
    }

    @MainActor
    func testALeadOnAPeerIsClearedThroughTheComputerThatReachesIt() async throws {
        KeychainStore.removeAll()
        ProjectActionProtocol.state.reset()
        try AgenthailIOSTests.pair("hlaptop", "https://laptop.invalid", name: "laptop")
        let model = AgenthailIOSModel(autoConnect: false, session: FixtureScope.open(self).session([ProjectActionProtocol.self]))
        defer { KeychainStore.removeAll() }
        let local = #"{"id":"hlaptop","name":"laptop","state":"local"}"#
        let peer = #"{"id":"hserver","name":"server","state":"connected"}"#
        let json = #"{"updatedAt":"now","daemon":{"running":true,"hosts":[\#(peer)]},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"codexRecentHours":24,"projects":[{"id":"\#(Self.repo)","displayName":"widget","checkouts":[{"host":\#(local),"projectId":"\#(Self.repo)","rootPath":"/w","added":true},{"host":\#(peer),"projectId":"hserver::\#(Self.repo)","rootPath":"/srv/w","added":true}],"coordinators":[{"host":\#(local),"name":"front","surface":"claude"},{"host":\#(peer),"name":"back","surface":"codex"}]}]}"#
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(json.utf8)), host: "hlaptop")
        let project = try XCTUnwrap(model.projects[Self.repo])

        model.settle(await model.clearLead(of: project))
        XCTAssertNil(model.operationError)
        XCTAssertEqual(Set(ProjectActionProtocol.state.take().map { "\($0.host) \($0.action) \($0.projectID)" }),
                       ["laptop.invalid coordinator-clear \(Self.repo)", "laptop.invalid coordinator-clear hserver::\(Self.repo)"])

        model.settle(await model.renameProject(project, to: "Widget"))
        XCTAssertEqual(Set(ProjectActionProtocol.state.take().map { "\($0.host) \($0.action) \($0.projectID)" }),
                       ["laptop.invalid project-rename \(Self.repo)", "laptop.invalid project-rename hserver::\(Self.repo)"])
    }

    @MainActor
    func testAComputerThatRefusesIsNamedAndTheOthersStillChange() async throws {
        let model = try pairedModel()
        defer { KeychainStore.removeAll() }
        ProjectActionProtocol.state.refuse("server.invalid")
        let project = try XCTUnwrap(model.projects[Self.repo])
        model.settle(await model.renameProject(project, to: "Widget"))
        XCTAssertEqual(model.operationError, "Renamed to Widget. Not changed on server: busy.")
    }
}

private final class ProjectActionProtocol: URLProtocol, @unchecked Sendable {
    struct Action { let host: String; let action: String; let projectID: String; let remoteHost: String? }
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var records: [Action] = []
        private var refusing = ""
        func reset() { lock.withLock { records = []; refusing = "" } }
        func refuse(_ host: String) { lock.withLock { refusing = host } }
        func take() -> [Action] { lock.withLock { defer { records = [] }; return records } }
        func record(_ action: Action) -> Bool { lock.withLock { records.append(action); return action.host != refusing } }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() { served(answer) }

    private func answer() {
        var status = 503
        var text = #"{"error":{"message":"unavailable"}}"#
        if request.url?.path == "/api/v1/actions" {
            var data = request.httpBody ?? Data()
            if let stream = request.httpBodyStream {
                stream.open()
                defer { stream.close() }
                var buffer = [UInt8](repeating: 0, count: 4096)
                while stream.hasBytesAvailable {
                    let count = stream.read(&buffer, maxLength: buffer.count)
                    if count <= 0 { break }
                    data.append(contentsOf: buffer.prefix(count))
                }
            }
            let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
            let action = Action(host: request.url?.host ?? "", action: body["action"] as? String ?? "", projectID: body["projectId"] as? String ?? "", remoteHost: body["host"] as? String)
            if Self.state.record(action) {
                status = 200
                text = action.action == "coordinator-new" ? #"{"coordinator":{"name":"lead","projectRoot":"/srv/widget"}}"# : #"{"ok":true}"#
            } else {
                (status, text) = (409, #"{"error":{"message":"busy"}}"#)
            }
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
