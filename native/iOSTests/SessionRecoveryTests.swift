import XCTest
@testable import Agenthail

@MainActor
final class SessionRecoveryTests: XCTestCase {
    func testReconnectReplacesStaleSnapshotAndRefreshesSelectedSession() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        await model.loadSession("demo")
        RecoveryProtocol.state.configure(stale: true)
        let staleResult = await model.refresh()
        XCTAssertTrue(staleResult)
        XCTAssertEqual(model.connectionError, "Agent catalog refresh failed")
        let before = RecoveryProtocol.state.sessionReads
        RecoveryProtocol.state.configure(stale: false)
        model.reconnecting = true
        await model.eventStreamConnected()
        XCTAssertNil(model.connectionError)
        XCTAssertFalse(model.reconnecting)
        XCTAssertEqual(RecoveryProtocol.state.sessionReads, before + 1)
        XCTAssertEqual(model.selectedDetail?.session.id, "demo")
        XCTAssertEqual(model.snapshot?.daemon.stale, false)
    }

    func testQueuedReceiptFollowsExpirationWithoutResending() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        try await sendQueuedInstruction(model)
        XCTAssertEqual(model.deliveryStatus["demo"], "Queued for the agent")
        RecoveryProtocol.state.configure(queueStatus: "expired")
        await model.eventStreamConnected()
        XCTAssertEqual(model.deliveryStatus["demo"], "Instruction expired. You can review it in Inbox history.")
        XCTAssertEqual(RecoveryProtocol.state.actionCount, 1)
    }

    func testDeliveryRefreshFailureAndUnknownOutcomeStayExplicit() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        try await sendQueuedInstruction(model)
        RecoveryProtocol.state.configure(queueFailure: true)
        await model.refreshSession("demo")
        XCTAssertEqual(model.deliveryStatus["demo"], "Delivery status could not be refreshed. Check Inbox before retrying.")
        RecoveryProtocol.state.configure(queueStatus: "dead", evidence: "unknown", queueFailure: false)
        await model.refreshSession("demo")
        XCTAssertEqual(model.deliveryStatus["demo"], "Delivery needs review in Inbox. Check the session before sending again.")
        XCTAssertEqual(RecoveryProtocol.state.actionCount, 1)
    }

    func testExpiredDeadReceiptsStopDemandingReviewButKeepUnknownTruth() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        try await sendQueuedInstruction(model)
        RecoveryProtocol.state.configure(queueStatus: "dead", queueHistorical: false, evidence: "unknown")
        await model.refreshSession("demo")
        XCTAssertEqual(model.deliveryStatus["demo"], "Delivery needs review in Inbox. Check the session before sending again.")
        RecoveryProtocol.state.configure(queueHistorical: true)
        await model.refreshSession("demo")
        XCTAssertEqual(model.deliveryStatus["demo"], "Delivery outcome was never confirmed and later expired. Review it in Inbox history before sending again.")
        XCTAssertEqual(RecoveryProtocol.state.actionCount, 1)
    }

    func testExpiredKnownFailureReceiptStopsDemandingReview() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        try await sendQueuedInstruction(model)
        RecoveryProtocol.state.configure(queueStatus: "dead", queueHistorical: true, evidence: "failed")
        await model.refreshSession("demo")
        XCTAssertEqual(model.deliveryStatus["demo"], "Delivery failed; the queue entry has expired. Review it in Inbox history.")
        XCTAssertEqual(RecoveryProtocol.state.actionCount, 1)
    }

    private func sendQueuedInstruction(_ model: AgenthailIOSModel) async throws {
        await model.loadSession("demo")
        _ = await model.refresh()
        model.composer = "Keep the regression test"
        model.send(to: model.snapshot!.sessions[0])
        for _ in 0..<100 where model.sendingSessionIDs.contains("demo") { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertFalse(model.sendingSessionIDs.contains("demo"))
    }

    private func makeModel() -> AgenthailIOSModel {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RecoveryProtocol.self]
        return AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration)))
    }
}

private final class RecoveryProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var stale = false
        private var queueStatus = "pending"
        private var queueHistorical: Bool?
        private var evidence: String?
        private var queueFailure = false
        private var reads = 0
        private var actions = 0
        var sessionReads: Int { lock.withLock { reads } }
        var actionCount: Int { lock.withLock { actions } }
        func reset() { lock.withLock { stale = false; queueStatus = "pending"; queueHistorical = nil; evidence = nil; queueFailure = false; reads = 0; actions = 0 } }
        func configure(stale: Bool? = nil, queueStatus: String? = nil, queueHistorical: Bool? = nil, evidence: String? = nil, queueFailure: Bool? = nil) {
            lock.withLock {
                if let stale { self.stale = stale }
                if let queueStatus { self.queueStatus = queueStatus }
                if let queueHistorical { self.queueHistorical = queueHistorical }
                if let evidence { self.evidence = evidence }
                if let queueFailure { self.queueFailure = queueFailure }
            }
        }
        func response(path: String) -> (Int, String) {
            lock.withLock {
                switch path {
                case "/api/v1/session":
                    reads += 1
                    return (200, SessionPreview.detailJSON)
                case "/api/v1/snapshot":
                    var snapshot = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
                    snapshot["daemon"] = ["running": true, "stale": stale, "refreshError": "Agent catalog refresh failed"]
                    return (200, String(data: try! JSONSerialization.data(withJSONObject: snapshot), encoding: .utf8)!)
                case "/api/v1/actions":
                    actions += 1
                    return (200, #"{"ok":true,"result":{"evidence":"queued","queueId":7}}"#)
                case "/api/v1/queue":
                    if queueFailure { return (503, #"{"error":{"message":"Queue unavailable"}}"#) }
                    var fields = ""
                    if let queueHistorical { fields += ",\"historical\":\(queueHistorical)" }
                    fields += ",\"evidence\":\"\(evidence ?? (queueStatus == "pending" || queueStatus == "inflight" ? "queued" : queueStatus))\""
                    return (200, "{\"items\":[{\"id\":7,\"sessionId\":\"demo\",\"target\":\"demo\",\"message\":\"Keep the regression test\",\"status\":\"\(queueStatus)\",\"attempts\":0,\"queuedAt\":\"2026-09-12 04:00:00\"\(fields)}]}")
                default: return (404, "{}")
                }
            }
        }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let (status, body) = Self.state.response(path: request.url!.path)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
