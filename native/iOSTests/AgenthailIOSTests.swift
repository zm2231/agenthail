import XCTest
@testable import Agenthail

final class AgenthailIOSTests: XCTestCase {
    func testEmptyEventStreamMarksConnectionHealthy() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [EmptyEventStreamURLProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "https://mac.tailnet.ts.net")!, token: "token", session: URLSession(configuration: configuration))
        let probe = EventConnectionProbe()
        do {
            try await api.streamEvents(
                after: 0,
                onConnected: { await probe.markConnected() },
                onEvent: { _ in await probe.markEvent() }
            )
            XCTFail("closed event stream unexpectedly returned")
        } catch AgenthailAPIError.streamClosed {
        }
        let result = await probe.result()
        XCTAssertTrue(result.connected)
        XCTAssertEqual(result.events, 0)
    }

    @MainActor
    func testEventRefreshDoesNotRestorePreviousSelection() async {
        let probe = IOSSelectionProbe()
        probe.selectedID = "A"
        async let stale: Void = probe.refresh("A", delay: .milliseconds(120))
        probe.selectedID = "B"
        async let current: Void = probe.refresh("B", delay: .milliseconds(10))
        _ = await (stale, current)
        XCTAssertEqual(probe.selectedID, "B")
        XCTAssertEqual(probe.detailID, "B")
    }

    @MainActor
    func testEventStreamOnlyReportsConfirmedOutage() {
        let model = AgenthailIOSModel(autoConnect: false)
        model.connectionError = "old"
        model.recordStreamInterruption(probeError: nil)
        XCTAssertTrue(model.reconnecting)
        XCTAssertNil(model.connectionError)
        model.recordStreamInterruption(probeError: NSError(domain: "network", code: -1, userInfo: [NSLocalizedDescriptionKey: "offline"]))
        XCTAssertFalse(model.reconnecting)
        XCTAssertEqual(model.connectionError, "offline")
    }

    @MainActor
    func testForegroundConnectionRetriesAfterTransientOutage() async throws {
        KeychainStore.removeAll()
        try KeychainStore.set("https://mac.tailnet.ts.net", account: "endpoint")
        try KeychainStore.set("token", account: "token")
        FlakyConnectionURLProtocol.state.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [FlakyConnectionURLProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        model.resumeConnection()
        for _ in 0..<150 where !FlakyConnectionURLProtocol.state.connected {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertGreaterThanOrEqual(FlakyConnectionURLProtocol.state.versionRequests, 2)
        XCTAssertTrue(FlakyConnectionURLProtocol.state.connected)
        XCTAssertNotNil(model.snapshot)
        model.forgetThisMac()
        KeychainStore.removeAll()
    }

    @MainActor
    func testIncompatibleProtocolDoesNotRetry() async throws {
        KeychainStore.removeAll()
        try KeychainStore.set("https://mac.tailnet.ts.net", account: "endpoint")
        try KeychainStore.set("token", account: "token")
        IncompatibleConnectionURLProtocol.requests.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [IncompatibleConnectionURLProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        model.connect()
        try await Task.sleep(for: .milliseconds(1200))
        XCTAssertEqual(IncompatibleConnectionURLProtocol.requests.snapshot().count, 1)
        XCTAssertFalse(model.reconnecting)
        model.forgetThisMac()
        KeychainStore.removeAll()
    }

    func testPairingLinksRequireSecureTailscaleEndpoint() throws {
        XCTAssertThrowsError(try PairingLink(url: URL(string: "agenthail://pair?endpoint=http%3A%2F%2Fmac.tailnet.ts.net%3A7412&secret=12345678901234567890")!))
        XCTAssertThrowsError(try PairingLink(url: URL(string: "agenthail://pair?endpoint=https%3A%2F%2Fattacker.example&secret=12345678901234567890")!))
        let valid = try PairingLink(url: URL(string: "agenthail://pair?endpoint=https%3A%2F%2Fmac.tailnet.ts.net%3A7412&secret=12345678901234567890")!)
        XCTAssertEqual(valid.endpoint.host, "mac.tailnet.ts.net")
    }

    @MainActor
    func testExistingPairingRequiresReplacementConfirmation() throws {
        KeychainStore.removeAll()
        try KeychainStore.set("https://old.tailnet.ts.net:7412", account: "endpoint")
        try KeychainStore.set("token", account: "token")
        let model = AgenthailIOSModel(autoConnect: false)
        model.handlePairingURL(URL(string: "agenthail://pair?endpoint=https%3A%2F%2Fnew.tailnet.ts.net%3A7412&secret=12345678901234567890")!)
        XCTAssertTrue(model.isPaired)
        XCTAssertTrue(model.showPairingConfirmation)
        XCTAssertEqual(model.pairingConfirmationTitle, "Replace connected Mac?")
        XCTAssertTrue(model.pairingConfirmationMessage.contains("new.tailnet.ts.net"))
        KeychainStore.removeAll()
    }

    @MainActor
    func testReplacingMacRevokesOldDeviceAndRelayRegistration() async throws {
        KeychainStore.removeAll()
        try KeychainStore.set("https://old.tailnet.ts.net:7412", account: "endpoint")
        try KeychainStore.set("old-token", account: "token")
        try KeychainStore.set("https://relay.example", account: "pushRelayURL")
        let registration = PushRegistration(installationId: "old-installation", credential: "old-credential", expiresAt: Int64.max)
        try KeychainStore.set(try JSONEncoder().encode(registration).base64EncodedString(), account: "pushRegistration")
        ReplacementURLProtocol.recorder.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ReplacementURLProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        model.handlePairingURL(URL(string: "agenthail://pair?endpoint=https%3A%2F%2Fnew.tailnet.ts.net%3A7412&secret=12345678901234567890")!)
        model.confirmPairing()
        for _ in 0..<100 where KeychainStore.get("token") != "new-token" {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertEqual(KeychainStore.get("token"), "new-token")
        XCTAssertNil(KeychainStore.get("pushRegistration"))
        let requests = ReplacementURLProtocol.recorder.snapshot()
        XCTAssertTrue(requests.contains("DELETE relay.example/v1/register"))
        XCTAssertTrue(requests.contains("DELETE old.tailnet.ts.net/api/v1/device/push"))
        XCTAssertTrue(requests.contains("DELETE old.tailnet.ts.net/api/v1/device"))
        KeychainStore.removeAll()
    }

    func testPushRegistrationRenewsBeforeExpiry() {
        let now = Date().timeIntervalSince1970 * 1000
        XCTAssertTrue(PushRegistration(installationId: "old", credential: "secret", expiresAt: Int64(now + 6 * 24 * 60 * 60 * 1000)).needsRenewal)
        XCTAssertFalse(PushRegistration(installationId: "fresh", credential: "secret", expiresAt: Int64(now + 8 * 24 * 60 * 60 * 1000)).needsRenewal)
        XCTAssertTrue(PushRegistration(installationId: "legacy", credential: "secret", expiresAt: nil).needsRenewal)
    }

    func testPushRelayChecksStoredCredential() async throws {
        ReplacementURLProtocol.recorder.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ReplacementURLProtocol.self]
        let relay = PushRelayClient(baseURL: URL(string: "https://relay.example")!, session: URLSession(configuration: configuration))
        try await relay.validate(PushRegistration(installationId: "installation", credential: "credential", expiresAt: Int64.max))
        XCTAssertTrue(ReplacementURLProtocol.recorder.snapshot().contains("POST relay.example/v1/register/check"))
    }

    @MainActor
    func testForgetThisMacClearsLocalPairingWhileOffline() throws {
        KeychainStore.removeAll()
        try KeychainStore.set("https://unreachable.invalid", account: "endpoint")
        try KeychainStore.set("token", account: "token")
        try KeychainStore.set("registration", account: "pushRegistration")
        let model = AgenthailIOSModel()
        model.forgetThisMac()
        XCTAssertNil(KeychainStore.get("endpoint"))
        XCTAssertNil(KeychainStore.get("token"))
        XCTAssertNil(KeychainStore.get("pushRegistration"))
        XCTAssertFalse(model.isPaired)
    }
}

@MainActor
private final class IOSSelectionProbe {
    var selectedID: String?
    var detailID: String?

    func refresh(_ id: String, delay: Duration) async {
        try? await Task.sleep(for: delay)
        guard sessionLoadIsCurrent(id, selectedID: selectedID) else { return }
        detailID = id
    }
}

private actor EventConnectionProbe {
    private var connected = false
    private var events = 0

    func markConnected() {
        connected = true
    }

    func markEvent() {
        events += 1
    }

    func result() -> (connected: Bool, events: Int) {
        (connected, events)
    }
}

private final class EmptyEventStreamURLProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: 200,
            httpVersion: "HTTP/1.1",
            headerFields: ["Content-Type": "text/event-stream"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

private final class FlakyConnectionState: @unchecked Sendable {
    private let lock = NSLock()
    private var versions = 0
    private var streamConnected = false

    var versionRequests: Int {
        lock.lock()
        defer { lock.unlock() }
        return versions
    }

    var connected: Bool {
        lock.lock()
        defer { lock.unlock() }
        return streamConnected
    }

    func nextVersionRequest() -> Int {
        lock.lock()
        defer { lock.unlock() }
        versions += 1
        return versions
    }

    func markConnected() {
        lock.lock()
        streamConnected = true
        lock.unlock()
    }

    func reset() {
        lock.lock()
        versions = 0
        streamConnected = false
        lock.unlock()
    }
}

private final class FlakyConnectionURLProtocol: URLProtocol, @unchecked Sendable {
    static let state = FlakyConnectionState()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let path = request.url?.path ?? ""
        if path == "/api/v1/version", Self.state.nextVersionRequest() == 1 {
            respond(status: 503, body: #"{"error":"offline"}"#)
            return
        }
        if path == "/api/v1/version" {
            respond(status: 200, body: #"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#)
            return
        }
        if path == "/api/v1/snapshot" {
            respond(status: 200, body: #"{"updatedAt":"now","eventCursor":0,"daemon":{"running":true},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5}"#)
            return
        }
        if path == "/api/v1/events" {
            Self.state.markConnected()
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
            return
        }
        respond(status: 404, body: #"{"error":"not found"}"#)
    }

    override func stopLoading() {}

    private func respond(status: Int, body: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

private final class IncompatibleConnectionURLProtocol: URLProtocol, @unchecked Sendable {
    static let requests = RequestRecorder()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.requests.record(request)
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        let body = #"{"protocol":2,"minimumProtocol":2,"maximumProtocol":2}"#
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

private final class RequestRecorder: @unchecked Sendable {
    private let lock = NSLock()
    private var requests: [String] = []

    func record(_ request: URLRequest) {
        lock.lock()
        defer { lock.unlock() }
        requests.append("\(request.httpMethod ?? "GET") \(request.url?.host ?? "")\(request.url?.path ?? "")")
    }

    func snapshot() -> [String] {
        lock.lock()
        defer { lock.unlock() }
        return requests
    }

    func reset() {
        lock.lock()
        defer { lock.unlock() }
        requests.removeAll()
    }
}

private final class ReplacementURLProtocol: URLProtocol, @unchecked Sendable {
    static let recorder = RequestRecorder()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.recorder.record(request)
        let paired = request.url?.path == "/api/v1/pair"
        let body = paired
            ? #"{"device":{"id":"new-device","name":"Phone","scopes":["read","control"],"createdAt":"now","pushEnabled":false},"token":"new-token","protocol":1}"#
            : #"{"ok":true}"#
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
