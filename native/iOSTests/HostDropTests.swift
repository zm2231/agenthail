import XCTest
@testable import Agenthail

@MainActor
final class HostDropTests: XCTestCase {
    private func connectedModel() async throws -> AgenthailIOSModel {
        KeychainStore.removeAll()
        try AgenthailIOSTests.pair("hdrop", "https://drop.tailnet.ts.net", token: "fixture")
        HostDropProtocol.state.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [HostDropProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        model.connect()
        for _ in 0..<200 where !(HostDropProtocol.state.streaming && model.snapshot?.sessions.first?.host?.state == "connected") {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertEqual(model.snapshot?.sessions.first?.host?.state, "connected")
        return model
    }

    private func states(of model: AgenthailIOSModel, for duration: Duration) async throws -> [String] {
        var seen: [String] = []
        let clock = ContinuousClock()
        let end = clock.now + duration
        while clock.now < end {
            let state = model.snapshot?.sessions.first?.host?.state ?? "none"
            if seen.last != state { seen.append(state) }
            try await Task.sleep(for: .milliseconds(10))
        }
        return seen
    }

    func testOneFailedProbeAfterAStreamDropDoesNotMarkTheComputerDisconnected() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        HostDropProtocol.state.failNextProbes(1)
        HostDropProtocol.state.dropStreams()
        let seen = try await states(of: model, for: .seconds(3))
        XCTAssertEqual(seen, ["connected"], "a single failed probe flipped every session: \(seen)")
        XCTAssertTrue(HostDropProtocol.state.streaming, "the stream reconnects")
    }

    func testOneFailedProbeWhenReturningToTheForegroundDoesNotMarkTheComputerDisconnected() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        HostDropProtocol.state.failNextProbes(1)
        model.resumeConnection()
        let seen = try await states(of: model, for: .seconds(3))
        XCTAssertEqual(seen, ["connected"], "a single failed probe on resume flipped every session: \(seen)")
    }

    func testAComputerThatStaysDownPastTheGraceIsMarkedUnreachable() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        model.connection("hdrop")!.grace.window = 1
        HostDropProtocol.state.setDown(true)
        HostDropProtocol.state.dropStreams()
        let seen = try await states(of: model, for: .seconds(4))
        XCTAssertEqual(seen, ["connected", "unreachable"])
        XCTAssertNotNil(model.connection("hdrop")!.connectionError)
    }

    func testARefusedProbeMarksTheComputerUnreachableAtOnce() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        HostDropProtocol.state.setUnauthorized(true)
        HostDropProtocol.state.dropStreams()
        let seen = try await states(of: model, for: .milliseconds(800))
        XCTAssertEqual(seen, ["connected", "unreachable"], "a 401 is not a network blip")
    }

    func testRepeatedResumesDoNotKeepADownComputerShownConnected() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        model.connection("hdrop")!.grace.window = 1
        HostDropProtocol.state.setDown(true)
        let clock = ContinuousClock()
        let end = clock.now + .seconds(3)
        while clock.now < end, model.snapshot?.sessions.first?.host?.state != "unreachable" {
            model.resumeConnection()
            try await Task.sleep(for: .milliseconds(150))
        }
        XCTAssertEqual(model.snapshot?.sessions.first?.host?.state, "unreachable", "resuming again and again must not extend the grace")
    }

    func testARecoveredStreamRestoresRealReachabilityEvenIfItsRefreshFails() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        let connection = try XCTUnwrap(model.connection("hdrop"))
        HostDropProtocol.state.setDown(true)
        HostDropProtocol.state.dropStreams()
        for _ in 0..<100 where !connection.holding { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(connection.holding)
        XCTAssertFalse(connection.reachable)
        HostDropProtocol.state.setSnapshotFails(true)
        HostDropProtocol.state.setDown(false)
        for _ in 0..<300 where !HostDropProtocol.state.streaming { try await Task.sleep(for: .milliseconds(10)) }
        for _ in 0..<100 where connection.holding { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(connection.reachable, "an open event stream is real contact")
        XCTAssertFalse(connection.holding)
    }

    func testSubagentsThatFailedWhileHeldReloadOnceAndDoNotLoop() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        let connection = try XCTUnwrap(model.connection("hdrop"))
        var parent = try XCTUnwrap(model.snapshot?.sessions.first)
        parent.hiddenSubagents = SubagentRollup(count: 1, working: 0)
        await model.loadSubagents(of: parent)
        XCTAssertNil(model.fetchedSubagents.errors[parent.id])

        HostDropProtocol.state.setDown(true)
        HostDropProtocol.state.dropStreams()
        for _ in 0..<100 where !connection.holding { try await Task.sleep(for: .milliseconds(10)) }
        await model.loadSubagents(of: parent)
        XCTAssertNotNil(model.fetchedSubagents.errors[parent.id])

        HostDropProtocol.state.setSubagentsFail(true)
        let failed = model.fetchedSubagents.revision(parent.id)
        HostDropProtocol.state.setDown(false)
        for _ in 0..<300 where model.fetchedSubagents.revision(parent.id) == failed { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertNotEqual(model.fetchedSubagents.revision(parent.id), failed, "recovery reloads the failed list")

        await model.loadSubagents(of: parent)
        XCTAssertNotNil(model.fetchedSubagents.errors[parent.id])
        let settled = model.fetchedSubagents.revision(parent.id)
        for _ in 0..<3 { _ = await model.refresh(); try await Task.sleep(for: .milliseconds(100)) }
        XCTAssertEqual(model.fetchedSubagents.revision(parent.id), settled, "a list failing on a reachable computer is not refetched in a loop")
    }

    func testASendDuringTheGraceReportsItsOwnError() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        let listed = try XCTUnwrap(model.snapshot?.sessions.first)
        await model.loadSession(listed.id)
        XCTAssertEqual(model.selectedDetail?.session.id, listed.id)
        HostDropProtocol.state.setDown(true)
        HostDropProtocol.state.dropStreams()
        try await Task.sleep(for: .milliseconds(200))
        let session = try XCTUnwrap(model.snapshot?.sessions.first)
        XCTAssertEqual(session.host?.state, "connected")
        XCTAssertNil(model.connectionState(for: session.id).error, "the held computer raises no banner")
        model.composer = "Keep the regression test"
        model.send(to: session)
        for _ in 0..<200 where model.sendingSessionIDs.contains(session.id) { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertEqual(model.deliveryStatus[session.id], "Couldn’t complete request. Draft kept.")
    }

    func testTheInboxAsksAComputerHeldThroughTheGraceAndReportsItsOwnError() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        HostDropProtocol.state.setDown(true)
        HostDropProtocol.state.dropStreams()
        let connection = try XCTUnwrap(model.connection("hdrop"))
        for _ in 0..<200 where connection.reachable { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(connection.holding && !connection.reachable, "the computer is held through the grace")
        do {
            _ = try await model.inboxPage(sessionID: nil)
            XCTFail("a down computer answered the inbox")
        } catch AgenthailAPIError.unavailable(let message) {
            XCTFail("the inbox refused a computer shown connected without asking it: \(message)")
        } catch {
            XCTAssertTrue(error is URLError, "the inbox reports the computer's own error, got \(error)")
        }
    }

    func testSearchAndNewSessionsKeepAComputerHeldThroughTheGrace() async throws {
        let model = try await connectedModel()
        defer { model.forgetHost("hdrop"); KeychainStore.removeAll() }
        HostDropProtocol.state.setDown(true)
        HostDropProtocol.state.dropStreams()
        let connection = try XCTUnwrap(model.connection("hdrop"))
        for _ in 0..<200 where connection.reachable { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(connection.holding && !connection.reachable, "the computer is held through the grace")
        XCTAssertEqual(model.creationHosts.first { $0.id == "hdrop" }?.state, "connected", "new sessions stopped offering a computer shown connected")
        await model.searchSessions("regression")
        XCTAssertTrue((model.searchError ?? "").contains(connection.name), "search skipped a computer shown connected instead of reporting its error: \(model.searchError ?? "nil")")
    }
}

final class HostDropProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var open: [HostDropProtocol] = []
        private var failing = 0
        private var downNow = false
        private var refused = false
        private var snapshotsFail = false
        private var subagentsFail = false
        func reset() { lock.withLock { open = []; failing = 0; downNow = false; refused = false; snapshotsFail = false; subagentsFail = false } }
        func setSnapshotFails(_ value: Bool) { lock.withLock { snapshotsFail = value } }
        func setSubagentsFail(_ value: Bool) { lock.withLock { subagentsFail = value } }
        func fails(snapshotFor query: String?) -> Bool {
            lock.withLock { (query?.contains("parentId") == true) ? subagentsFail : snapshotsFail }
        }
        func setDown(_ value: Bool) { lock.withLock { downNow = value } }
        func setUnauthorized(_ value: Bool) { lock.withLock { refused = value } }
        var down: Bool { lock.withLock { downNow } }
        var unauthorized: Bool { lock.withLock { refused } }
        var streaming: Bool { lock.withLock { !open.isEmpty } }
        func failNextProbes(_ count: Int) { lock.withLock { failing = count } }
        func takeProbeFailure() -> Bool { lock.withLock { guard failing > 0 else { return false }; failing -= 1; return true } }
        func opened(_ stream: HostDropProtocol) { lock.withLock { open.append(stream) } }
        func closed(_ stream: HostDropProtocol) { lock.withLock { open.removeAll { $0 === stream } } }
        func dropStreams() {
            let dropped = lock.withLock { defer { open = [] }; return open }
            for stream in dropped { stream.client?.urlProtocol(stream, didFailWithError: URLError(.networkConnectionLost)) }
        }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let path = request.url!.path
        if Self.state.down {
            client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
            return
        }
        if Self.state.unauthorized {
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 401, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(#"{"error":{"message":"This device is no longer paired."}}"#.utf8))
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        if path == "/api/v1/events" {
            Self.state.opened(self)
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
            return
        }
        if path == "/api/v1/snapshot", Self.state.fails(snapshotFor: request.url!.query) {
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 500, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(#"{"error":{"message":"busy"}}"#.utf8))
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        if path == "/api/v1/version", Self.state.takeProbeFailure() {
            client?.urlProtocol(self, didFailWithError: URLError(.timedOut))
            return
        }
        let body: String
        switch path {
        case "/api/v1/version": body = #"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#
        case "/api/v1/snapshot": body = SessionPreview.snapshotJSON
        case "/api/v1/session": body = SessionPreview.detailJSON
        default: body = "{}"
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() { Self.state.closed(self) }
}
