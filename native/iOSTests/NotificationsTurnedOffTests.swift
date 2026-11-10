import XCTest
@testable import Agenthail

@MainActor
final class NotificationsTurnedOffTests: XCTestCase {
    private let suite = "agenthail-tests-notifications-off"

    override func setUp() {
        KeychainStore.removeAll()
        UserDefaults().removePersistentDomain(forName: suite)
        PushRecordingProtocol.log.reset()
    }

    override func tearDown() {
        KeychainStore.removeAll()
        UserDefaults().removePersistentDomain(forName: suite)
    }

    func testTurningNotificationsOffSurvivesARelaunchAndAReconnect() async throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        try pairMac()
        let first = AgenthailIOSModel(autoConnect: false, session: session(), drops: nil, notificationDefaults: defaults)
        first.turnOffNotifications()
        for _ in 0..<200 where !PushRecordingProtocol.log.contains("DELETE /api/v1/device/push") {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertTrue(PushRecordingProtocol.log.contains("DELETE /api/v1/device/push"), "turning off removes the computer's push target")
        PushRecordingProtocol.log.reset()

        let relaunched = AgenthailIOSModel(autoConnect: false, session: session(), drops: nil, notificationDefaults: defaults)
        defer { relaunched.forgetHost("hmac") }
        try await connect(relaunched)
        relaunched.registerPushToken("a1b2c3")
        XCTAssertEqual(relaunched.notificationStatus, "Not enabled", "a device token does not start setup")
        relaunched.setAppActive(true)
        XCTAssertEqual(relaunched.notificationStatus, "Not enabled", "coming back to the app does not start setup")
        for _ in 0..<200 where !PushRecordingProtocol.log.contains("POST /v1/attest/challenge") {
            try await Task.sleep(for: .milliseconds(10))
        }

        XCTAssertFalse(PushRecordingProtocol.log.contains("PUT /api/v1/device/push"), "no push target is set again: \(PushRecordingProtocol.log.all)")
        XCTAssertFalse(PushRecordingProtocol.log.contains("POST /v1/attest/challenge"), "no relay registration starts: \(PushRecordingProtocol.log.all)")
    }

    func testWithoutTurningOffAConnectedComputerStartsSetup() async throws {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        try pairMac()
        let model = AgenthailIOSModel(autoConnect: false, session: session(), drops: nil, notificationDefaults: defaults)
        defer { model.forgetHost("hmac") }
        try await connect(model)
        model.registerPushToken("a1b2c3")
        XCTAssertEqual(model.notificationStatus, "Enabling", "a device token starts setup at once")
        for _ in 0..<200 where !PushRecordingProtocol.log.contains("POST /v1/attest/challenge") {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertTrue(PushRecordingProtocol.log.contains("POST /v1/attest/challenge"), "setup reaches the relay when notifications were never turned off")
    }

    private func pairMac() throws {
        try PairedCredential.save(PairedHostCredential(hostID: "hmac", hostName: "mac", endpoint: URL(string: "https://mac.tailnet.ts.net")!, token: "token", pairedAt: Date()))
    }

    private func connect(_ model: AgenthailIOSModel) async throws {
        model.resumeConnection()
        for _ in 0..<300 where model.hosts.first?.reachable != true {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertEqual(model.hosts.first?.reachable, true, "the computer connects")
    }

    private func session() -> URLSession {
        FixtureScope.open(self).session([PushRecordingProtocol.self])
    }
}

private final class PushRecordingProtocol: URLProtocol, @unchecked Sendable {
    final class Log: @unchecked Sendable {
        private let lock = NSLock()
        private var lines: [String] = []
        var all: [String] { lock.withLock { lines } }
        func reset() { lock.withLock { lines = [] } }
        func add(_ line: String) { lock.withLock { lines.append(line) } }
        func contains(_ line: String) -> Bool { lock.withLock { lines.contains(line) } }
    }

    static let log = Log()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() { served(answer) }

    private func answer() {
        let path = request.url?.path ?? ""
        Self.log.add("\(request.httpMethod ?? "GET") \(path)")
        if answeredProjectActivity() { return }
        switch path {
        case "/api/v1/version":
            respond(200, #"{"protocol":2,"minimumProtocol":1,"maximumProtocol":2,"pushRelayUrl":"https://relay.invalid"}"#)
        case "/api/v1/snapshot":
            respond(200, #"{"updatedAt":"now","eventCursor":0,"daemon":{"running":true},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5}"#)
        case "/api/v1/events":
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        case "/api/v1/device/push":
            respond(200, "{}")
        default:
            respond(404, #"{"error":"not here"}"#)
        }
    }

    override func stopLoading() {}

    private func respond(_ status: Int, _ body: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
