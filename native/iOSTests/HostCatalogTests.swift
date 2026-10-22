import XCTest
@testable import Agenthail

final class HostCatalogTests: XCTestCase {
    func testAComputersOwnSessionsAreKeyedByItAndRoutedToIt() {
        let catalog = HostCatalog(hosts: [host("hlaptop", "laptop", reachable: true, sessions: [local("s1")])])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hlaptop::s1"])
        XCTAssertEqual(catalog.sessions.first?.host, SessionHost(id: "hlaptop", name: "laptop", state: "connected"))
        XCTAssertEqual(catalog.route(for: "hlaptop::s1"), SessionRoute(hostID: "hlaptop", sessionID: "s1"))
    }

    func testTheOwnersRowWinsOverAMirrorWhileTheOwnerIsReachable() {
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: true, sessions: [mirrored("hserver", "server", "s1", name: "mirror copy")]),
            host("hserver", "server", reachable: true, sessions: [local("s1", name: "owner copy")]),
        ])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hserver::s1"])
        XCTAssertEqual(catalog.sessions.first?.name, "owner copy")
        XCTAssertEqual(catalog.routes(for: "hserver::s1"), [
            SessionRoute(hostID: "hserver", sessionID: "s1"),
            SessionRoute(hostID: "hlaptop", sessionID: "hserver::s1"),
        ])
    }

    func testAnOwnerThePhoneCannotReachIsWorkedThroughAComputerThatMirrorsIt() {
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: true, sessions: [mirrored("hserver", "server", "s1", name: "mirror copy")]),
            host("hserver", "server", reachable: false, sessions: [local("s1", name: "owner copy")]),
        ])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hserver::s1"])
        XCTAssertEqual(catalog.sessions.first?.name, "mirror copy")
        XCTAssertEqual(catalog.sessions.first?.unavailableReason, nil)
        XCTAssertEqual(catalog.route(for: "hserver::s1"), SessionRoute(hostID: "hlaptop", sessionID: "hserver::s1"))
    }

    func testASessionIsUnreachableOnlyWhenNoComputerServingItIs() {
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: true, sessions: [local("mine")]),
            host("hserver", "server", reachable: false, sessions: [local("s1")]),
        ])
        let down = catalog.sessions.first { $0.id == "hserver::s1" }
        XCTAssertEqual(down?.unavailableReason, "server can't be reached")
        XCTAssertNil(catalog.route(for: "hserver::s1"))
        let up = catalog.sessions.first { $0.id == "hlaptop::mine" }
        XCTAssertNil(up?.unavailableReason)
        XCTAssertNotNil(catalog.route(for: "hlaptop::mine"))
    }

    func testAMirroredSessionOfAComputerThePhoneIsNotPairedWithKeepsItsQualifiedID() {
        let catalog = HostCatalog(hosts: [host("hlaptop", "laptop", reachable: true, sessions: [mirrored("hbuild", "build", "s9")])])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hbuild::s9"])
        XCTAssertEqual(catalog.sessions.first?.host?.name, "build")
        XCTAssertEqual(catalog.route(for: "hbuild::s9"), SessionRoute(hostID: "hlaptop", sessionID: "hbuild::s9"))
    }

    func testSubagentsAndProjectsOfALocalRowAreQualifiedWithItsComputer() {
        var child = local("child")
        child.subagent = SubagentIdentity(parentId: "parent", rootId: "parent", depth: 1)
        child.project = ProjectIdentity(id: "p1", displayName: "widget", rootPath: "/w", kind: "git")
        let catalog = HostCatalog(hosts: [host("hlaptop", "laptop", reachable: true, sessions: [child])])
        XCTAssertEqual(catalog.sessions.first?.subagent?.parentId, "hlaptop::parent")
        XCTAssertEqual(catalog.sessions.first?.subagent?.rootId, "hlaptop::parent")
        XCTAssertEqual(catalog.sessions.first?.project?.id, "hlaptop::p1")
    }

    func testDeliveryProblemsAndAttentionNameTheirComputer() {
        var snapshot = Self.snapshot([local("s1")])
        snapshot.deliveryProblems = [DeliveryProblem(deliveryId: 7, sessionId: "s1", sourceSessionId: "s2", attentionSessionId: "s2", message: "m", reason: "r", status: nil, at: "now")]
        let other = Self.snapshot([local("s1")], problems: [DeliveryProblem(deliveryId: 7, sessionId: "s1", sourceSessionId: nil, attentionSessionId: nil, message: "m", reason: "r", status: nil, at: "now")])
        let catalog = HostCatalog(hosts: [
            PairedHostView(id: "ha", name: "a", reachable: true, snapshot: snapshot),
            PairedHostView(id: "hb", name: "b", reachable: true, snapshot: other),
        ])
        XCTAssertEqual(catalog.deliveryProblems.map(\.sessionId), ["ha::s1", "hb::s1"])
        XCTAssertEqual(catalog.deliveryProblems.first?.sourceSessionId, "ha::s2")
        XCTAssertEqual(Set(catalog.deliveryProblems.map(\.id)).count, 2, "the same delivery id on two computers is two problems")
    }

    private func host(_ id: String, _ name: String, reachable: Bool, sessions: [SessionState]) -> PairedHostView {
        PairedHostView(id: id, name: name, reachable: reachable, snapshot: Self.snapshot(sessions))
    }

    private func local(_ id: String, name: String? = nil) -> SessionState {
        var row = Self.row(id, name: name ?? id)
        row.host = SessionHost(id: "self", name: "self", state: "local")
        return row
    }

    private func mirrored(_ owner: String, _ ownerName: String, _ id: String, name: String? = nil) -> SessionState {
        var row = Self.row(owner + "::" + id, name: name ?? id)
        row.host = SessionHost(id: owner, name: ownerName, state: "connected")
        return row
    }

    private static func row(_ id: String, name: String) -> SessionState {
        SessionState(id: id, surface: "claude", name: name, alias: nil, status: "idle", lastActive: nil, queueCount: 0,
                     open: true, current: true, currentReason: nil, capabilities: Capabilities(send: true, steer: true), readOnly: false, readOnlyReason: nil)
    }

    static func snapshot(_ sessions: [SessionState], problems: [DeliveryProblem]? = nil) -> DashboardSnapshot {
        DashboardSnapshot(updatedAt: "2026-10-22T12:00:00Z", eventCursor: 1, hostEpoch: "e", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: sessions, totalSessions: sessions.count, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: problems, codexRecentHours: 8, busyDelivery: "queue")
    }
}
