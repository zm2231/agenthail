import XCTest
@testable import Agenthail

extension ConnectionDrops {
    static func isolated() -> ConnectionDrops {
        let suite = "agenthail-tests-connection-drops"
        UserDefaults().removePersistentDomain(forName: suite)
        return ConnectionDrops(defaults: UserDefaults(suiteName: suite)!, watchesNetwork: false)
    }
}

@MainActor
final class ConnectionDropsTests: XCTestCase {
    private let start = Date(timeIntervalSince1970: 1_793_000_000)
    private let wifi = NetworkSnapshot(interface: "wifi", vpn: true, expensive: false, constrained: false)
    private var counter = 0

    private func nextID() -> String {
        counter += 1
        return String(format: "drop%08d", counter)
    }

    func testRetriesMergeIntoOneDropThatClosesWhenTheComputerAnswers() {
        var log = ConnectionDropLog()
        log.foregrounded(at: start)
        log.fail(host: "hstudio", path: "stream", error: "URLError.-1005", elapsed: nil, shown: .held, network: wifi, now: start.addingTimeInterval(3), id: nextID)
        log.fail(host: "hstudio", path: "stream probe", error: "URLError.-1001", elapsed: 15, shown: .offline, network: nil, now: start.addingTimeInterval(18), id: nextID)
        XCTAssertEqual(log.open.count, 1, "a retry opened a second drop")
        XCTAssertTrue(log.recover(host: "hstudio", by: "event stream", now: start.addingTimeInterval(20)))
        let drop = try! XCTUnwrap(log.closed.first)
        XCTAssertEqual(drop.path, "stream")
        XCTAssertEqual(drop.firstError, "URLError.-1005")
        XCTAssertEqual(drop.lastError, "URLError.-1001")
        XCTAssertEqual(drop.attempts, 2)
        XCTAssertEqual(drop.shown, .offline, "a drop the person saw as offline must say so even if it began held")
        XCTAssertEqual(drop.sinceForegroundMs, 3000)
        XCTAssertEqual(drop.network, "wifi")
        XCTAssertTrue(drop.vpn)
        XCTAssertNil(drop.elapsedMs)
        XCTAssertEqual(drop.recoveredBy, "event stream")
        XCTAssertNil(drop.via, "a drop of the phone's own link is direct")
        XCTAssertFalse(log.recover(host: "hstudio", by: "snapshot", now: start.addingTimeInterval(30)), "a recovery with nothing open closed something")
    }

    func testADropLongAfterReturningToTheAppIsSteadyUseAndBackgroundingIsRecorded() {
        var log = ConnectionDropLog()
        log.foregrounded(at: start)
        log.fail(host: "hstudio", path: "connect", error: "URLError.-1004", elapsed: 0.2, shown: .offline, network: wifi, now: start.addingTimeInterval(ConnectionDropLog.steadyAfter + 1), id: nextID)
        log.backgrounded()
        log.recover(host: "hstudio", by: "snapshot", now: start.addingTimeInterval(4000))
        let drop = log.closed[0]
        XCTAssertNil(drop.sinceForegroundMs, "a drop in steady use was tied to returning to the app")
        XCTAssertEqual(drop.elapsedMs, 200)
        XCTAssertTrue(drop.wentBackground, "time spent in the background was counted as network time")
        log.fail(host: "hstudio", path: "connect", error: "URLError.-1004", elapsed: nil, shown: .offline, network: nil, now: start.addingTimeInterval(5000), id: nextID)
        XCTAssertNil(log.open.values.first?.sinceForegroundMs, "a backgrounded app has no foreground to measure from")
    }

    func testARelayedDropIsKeptApartAndClosesWhenTheRelayReachesItsComputerAgain() {
        var log = ConnectionDropLog()
        log.fail(host: "hstudio", path: "request", error: "URLError.-1001", elapsed: 1, shown: .held, network: wifi, now: start, id: nextID)
        log.fail(host: "hbuild", via: "hstudio", path: "request", error: "relay.host_unreachable", elapsed: nil, shown: .offline, network: wifi, now: start, id: nextID)
        XCTAssertEqual(log.open.count, 2, "a relayed drop merged into the phone's own link")
        XCTAssertFalse(log.recoverRelays(via: "hstudio", connected: ["hother"], now: start.addingTimeInterval(5)))
        XCTAssertTrue(log.recoverRelays(via: "hstudio", connected: ["hbuild"], now: start.addingTimeInterval(9)))
        XCTAssertEqual(log.closed.map(\.host), ["hbuild"])
        XCTAssertEqual(log.closed[0].via, "hstudio")
        XCTAssertEqual(log.closed[0].recoveredBy, "peer connected")
        XCTAssertEqual(log.pending(for: "hstudio").map(\.host), ["hbuild"], "a relayed drop is reported to the computer that relayed it")
        XCTAssertEqual(log.pending(for: "hbuild"), [], "the unreachable computer was asked to take its own drop")
    }

    func testTheAppCancellingItsOwnRetryDoesNotHideTheRealError() {
        var log = ConnectionDropLog()
        log.fail(host: "hstudio", path: "stream", error: "URLError.-1001", elapsed: nil, shown: .held, network: wifi, now: start, id: nextID)
        log.fail(host: "hstudio", path: "stream probe", error: "URLError.-1005", elapsed: 2, shown: .offline, network: wifi, now: start.addingTimeInterval(3), id: nextID)
        log.fail(host: "hstudio", path: "request", error: "URLError.-999", elapsed: nil, shown: .offline, network: wifi, now: start.addingTimeInterval(4), id: nextID)
        log.fail(host: "hstudio", path: "request", error: "cancelled", elapsed: nil, shown: .offline, network: wifi, now: start.addingTimeInterval(5), id: nextID)
        XCTAssertEqual(log.open.values.first?.lastError, "URLError.-1005", "a cancellation replaced the error that took the computer offline")
        XCTAssertEqual(log.open.values.first?.attempts, 4, "a cancellation still counts as an attempt")

        log.fail(host: "hbuild", path: "request", error: "URLError.-999", elapsed: nil, shown: .offline, network: wifi, now: start, id: nextID)
        log.fail(host: "hbuild", path: "request", error: "cancelled", elapsed: nil, shown: .offline, network: wifi, now: start.addingTimeInterval(1), id: nextID)
        XCTAssertEqual(log.open.values.first { $0.host == "hbuild" }?.lastError, "cancelled", "a drop with only cancellations keeps its latest")
        log.fail(host: "hbuild", path: "request", error: "URLError.-1009", elapsed: nil, shown: .offline, network: wifi, now: start.addingTimeInterval(2), id: nextID)
        XCTAssertEqual(log.open.values.first { $0.host == "hbuild" }?.lastError, "URLError.-1009", "a real error after cancellations is kept")
    }

    func testStoppingAndForgettingAComputerDropItsRows() {
        var log = ConnectionDropLog()
        log.fail(host: "hstudio", path: "connect", error: "URLError.-1004", elapsed: nil, shown: .offline, network: nil, now: start, id: nextID)
        log.recover(host: "hstudio", by: "snapshot", now: start.addingTimeInterval(1))
        log.fail(host: "hstudio", path: "connect", error: "URLError.-1004", elapsed: nil, shown: .offline, network: nil, now: start.addingTimeInterval(2), id: nextID)
        log.fail(host: "hbuild", via: "hstudio", path: "request", error: "relay.host_unreachable", elapsed: nil, shown: .offline, network: nil, now: start, id: nextID)
        log.fail(host: "hother", path: "connect", error: "URLError.-1004", elapsed: nil, shown: .offline, network: nil, now: start, id: nextID)
        log.stopped(host: "hstudio")
        XCTAssertEqual(log.open.values.map(\.host), ["hother"], "stopping a computer kept its open drops")
        XCTAssertEqual(log.closed.count, 1, "stopping a computer threw away drops still to report")
        log.forget(host: "hstudio")
        XCTAssertTrue(log.closed.isEmpty)
    }

    func testOnlyTheNewestDropsWithinAWeekAreKept() {
        var log = ConnectionDropLog()
        for index in 0..<(ConnectionDropLog.kept + 3) {
            let at = start.addingTimeInterval(Double(index))
            log.fail(host: "hstudio", path: "connect", error: "URLError.-1004", elapsed: nil, shown: .offline, network: nil, now: at, id: nextID)
            log.recover(host: "hstudio", by: "snapshot", now: at)
        }
        XCTAssertEqual(log.closed.count, ConnectionDropLog.kept)
        XCTAssertEqual(log.closed.first?.startedAt, start.addingTimeInterval(3), "the oldest drops were not the ones dropped")
        log.bound(now: start.addingTimeInterval(ConnectionDropLog.maxAge + 100))
        XCTAssertTrue(log.closed.isEmpty, "drops older than a week were kept")
    }

    func testErrorsAreReportedAsStableCodes() {
        XCTAssertEqual(ConnectionDropLog.code(URLError(.timedOut)), "URLError.-1001")
        XCTAssertEqual(ConnectionDropLog.code(AgenthailAPIError.request(503, "Host is unreachable")), "http.503")
        XCTAssertEqual(ConnectionDropLog.code(AgenthailAPIError.streamClosed), "streamClosed")
        XCTAssertEqual(ConnectionDropLog.code(AgenthailAPIError.unavailable("anything at all")), "unavailable")
        XCTAssertEqual(ConnectionDropLog.code(NSError(domain: "kCFErrorDomainCFNetwork", code: 310)), "kCFErrorDomainCFNetwork.310")
    }

    func testTheLogSurvivesARelaunch() throws {
        let suite = "connection-drops-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let drops = ConnectionDrops(defaults: defaults, watchesNetwork: false)
        drops.now = { self.start }
        drops.failed(host: "hstudio", path: "connect", error: "URLError.-1004", elapsed: nil, shown: .offline)
        XCTAssertTrue(drops.recovered(host: "hstudio", by: "snapshot"))
        let relaunched = ConnectionDrops(defaults: defaults, watchesNetwork: false)
        XCTAssertEqual(relaunched.log.pending(for: "hstudio").count, 1)
    }

    func testARelayFailureFromTheDaemonOpensADropThroughThatComputer() async throws {
        let suite = "connection-drops-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let drops = ConnectionDrops(defaults: defaults, watchesNetwork: false)
        let credential = PairedHostCredential(hostID: "hstudio", hostName: "studio", endpoint: URL(string: "https://studio.example.test")!, token: "token", pairedAt: Date())
        let connection = HostConnection(credential: credential, session: URLSession(configuration: .ephemeral), drops: drops)
        let response = HTTPURLResponse(url: credential.endpoint, statusCode: 503, httpVersion: nil, headerFields: nil)!
        let body = Data(#"{"error":{"code":"host_unreachable","message":"Host build is unreachable"},"host":"hbuild"}"#.utf8)
        XCTAssertThrowsError(try connection.api.validate(response: response, data: body))
        for _ in 0..<100 where drops.log.open.isEmpty { try await Task.sleep(for: .milliseconds(10)) }
        let drop = try XCTUnwrap(drops.log.open.values.first)
        XCTAssertEqual(drop.host, "hbuild")
        XCTAssertEqual(drop.via, "hstudio")
        XCTAssertEqual(drop.firstError, "relay.host_unreachable")
        let own = Data(#"{"error":{"code":"host_unreachable","message":"x"},"host":"hstudio"}"#.utf8)
        XCTAssertThrowsError(try connection.api.validate(response: response, data: own))
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertEqual(drops.log.open.count, 1, "a computer reported unreachable through itself")
    }
}

@MainActor
final class StreamDropElapsedTests: XCTestCase {
    func testAStreamDropRecordsHowLongTheStreamWasSilent() async throws {
        let drop = try await streamDrop(connects: true)
        XCTAssertEqual(drop.path, "stream")
        let elapsed = try XCTUnwrap(drop.elapsedMs, "a stream that had been up recorded no silence")
        XCTAssertGreaterThanOrEqual(elapsed, 150, "the stream was silent from its keepalive to its close")
        XCTAssertLessThan(elapsed, 1_000, "the silence was measured from connect, not from the keepalive")
    }

    func testAStreamThatNeverConnectedRecordsNoDuration() async throws {
        let drop = try await streamDrop(connects: false)
        XCTAssertEqual(drop.path, "stream")
        XCTAssertNil(drop.elapsedMs, "a stream that never connected has no silence to measure")
    }

    func testAReconnectDoesNotCarryTheOldStreamsUpTime() async throws {
        let drop = try await streamDrop(connects: true, reconnect: true)
        XCTAssertEqual(drop.path, "stream")
        XCTAssertNil(drop.elapsedMs, "the new stream never connected but took the old one's timing")
    }

    func testACutOffLineCountsAsHeard() async throws {
        let drop = try await streamDrop(connects: true, cutsOff: true)
        let elapsed = try XCTUnwrap(drop.elapsedMs)
        XCTAssertLessThan(elapsed, 1_000, "a keepalive cut off before its newline was not counted as heard")
    }

    private func streamDrop(connects: Bool, reconnect: Bool = false, cutsOff: Bool = false) async throws -> ConnectionDrop {
        StreamUpProtocol.state.reset(connects: connects, holds: reconnect, cutsOff: cutsOff)
        let suite = "connection-drops-\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let drops = ConnectionDrops(defaults: defaults, watchesNetwork: false)
        let credential = PairedHostCredential(hostID: "hstudio", hostName: "studio", endpoint: URL(string: "https://studio.example.test")!, token: "token", pairedAt: Date())
        let connection = HostConnection(credential: credential, session: FixtureScope.open(self).session([StreamUpProtocol.self]), drops: drops)
        defer { connection.stop() }
        connection.connect()
        if reconnect {
            for _ in 0..<250 where !StreamUpProtocol.state.held { try await Task.sleep(for: .milliseconds(20)) }
            XCTAssertTrue(StreamUpProtocol.state.held, "the first stream never connected")
            try await Task.sleep(for: .milliseconds(300))
            StreamUpProtocol.state.refuseStreams()
            connection.connect()
        }
        for _ in 0..<250 where drops.log.open.isEmpty { try await Task.sleep(for: .milliseconds(20)) }
        return try XCTUnwrap(drops.log.open.values.first { $0.path == "stream" }, "no stream drop was recorded")
    }
}

private final class StreamUpProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var connectsValue = true
        private var holdsValue = false
        private var heldValue = false
        private var droppedValue = false
        private var cutsOffValue = false
        var cutsOff: Bool { lock.withLock { cutsOffValue } }
        var connects: Bool { lock.withLock { connectsValue } }
        var holds: Bool { lock.withLock { holdsValue } }
        var held: Bool { lock.withLock { heldValue } }
        var dropped: Bool { lock.withLock { droppedValue } }
        func reset(connects: Bool, holds: Bool, cutsOff: Bool) { lock.withLock { connectsValue = connects; holdsValue = holds; cutsOffValue = cutsOff; heldValue = false; droppedValue = false } }
        func hold() { lock.withLock { heldValue = true } }
        func refuseStreams() { lock.withLock { connectsValue = false; holdsValue = false } }
        func drop() { lock.withLock { droppedValue = true } }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() { served(answer) }
    override func stopLoading() {}

    private func answer() {
        if answeredProjectActivity() { return }
        let path = request.url!.path
        if Self.state.dropped, path != "/api/v1/catalog-events" {
            respond(503, #"{"error":"offline"}"#, finish: true)
            return
        }
        switch path {
        case "/api/v1/version": respond(200, #"{"protocol":2,"minimumProtocol":1,"maximumProtocol":2}"#, finish: true)
        case "/api/v1/snapshot": respond(200, SessionPreview.snapshotJSON, finish: true)
        case "/api/v1/catalog-events": respond(200, ": connected\n\n", finish: false)
        case "/api/v1/events":
            guard Self.state.connects else {
                Self.state.drop()
                respond(503, #"{"error":"offline"}"#, finish: true)
                return
            }
            respond(200, ": connected\n\n", finish: false)
            if Self.state.holds {
                Self.state.hold()
                return
            }
            later(1.0) { self.client?.urlProtocol(self, didLoad: Data((Self.state.cutsOff ? ": keepal" : ": keepalive\n\n").utf8)) }
            later(1.2) {
                Self.state.drop()
                self.client?.urlProtocolDidFinishLoading(self)
            }
        default: respond(200, "{}", finish: true)
        }
    }

    private func respond(_ status: Int, _ body: String, finish: Bool) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        if finish { client?.urlProtocolDidFinishLoading(self) }
    }
}
