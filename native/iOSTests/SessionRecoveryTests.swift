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

    @MainActor
    func testOlderPageDoesNotMoveActiveJournalCursor() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        model.selectedSessionID = "demo"
        await model.refreshSession("demo")
        XCTAssertEqual(model.sessionStreamCursor, 2048)
        await model.loadOlderActivity()
        XCTAssertEqual(model.sessionStreamCursor, 2048)
    }

    @MainActor
    func testCatalogEpochResetsCursorToCurrentSnapshot() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        RecoveryProtocol.state.configureCatalog(epoch: "epoch-a", sequence: 41)
        let firstRefresh = await model.refresh()
        XCTAssertTrue(firstRefresh)
        XCTAssertEqual(model.catalogStreamCursor, 41)
        RecoveryProtocol.state.configureCatalog(epoch: "epoch-b", sequence: 2)
        let secondRefresh = await model.refresh()
        XCTAssertTrue(secondRefresh)
        XCTAssertEqual(model.catalogStreamCursor, 2)
    }

    @MainActor
    func testUnknownSurfaceHealthRefreshesFreshSnapshotOnce() async throws {
        RecoveryProtocol.state.reset()
        let model = makeModel()
        let initialRefresh = await model.refresh()
        XCTAssertTrue(initialRefresh)
        let before = RecoveryProtocol.state.freshSnapshotReads
        let event = try JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":1,"type":"surface.health","data":{"surface":"new-surface","health":"unavailable","detail":"discovery failed"}}"#.utf8))

        await model.receiveCatalog(event)

        XCTAssertEqual(RecoveryProtocol.state.freshSnapshotReads, before + 1)
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

    func testDeliveryProblemDismissalRemovesNoticeWithoutResending() async throws {
        RecoveryProtocol.state.reset()
        RecoveryProtocol.state.configure(deliveryProblem: true)
        let model = makeModel()
        let refreshed = await model.refresh()
        XCTAssertTrue(refreshed)
        let problem = try XCTUnwrap(model.deliveryProblems.first)
        XCTAssertEqual(problem.deliveryId, 42)

        try await model.dismissDeliveryProblem(problem)

        XCTAssertTrue(model.deliveryProblems.isEmpty)
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
        private var deliveryProblem = false
        private var catalogEpoch: String?
        private var catalogSequence: UInt64?
        private var reads = 0
        private var actions = 0
        private var actionHistory: [String] = []
        private var freshSnapshots = 0
        var sessionReads: Int { lock.withLock { reads } }
        var actionCount: Int { lock.withLock { actions } }
        var actionNames: [String] { lock.withLock { actionHistory } }
        var freshSnapshotReads: Int { lock.withLock { freshSnapshots } }
        func reset() { lock.withLock { stale = false; queueStatus = "pending"; queueHistorical = nil; evidence = nil; queueFailure = false; deliveryProblem = false; catalogEpoch = nil; catalogSequence = nil; reads = 0; actions = 0; actionHistory = []; freshSnapshots = 0 } }
        func configure(stale: Bool? = nil, queueStatus: String? = nil, queueHistorical: Bool? = nil, evidence: String? = nil, queueFailure: Bool? = nil, deliveryProblem: Bool? = nil) {
            lock.withLock {
                if let stale { self.stale = stale }
                if let queueStatus { self.queueStatus = queueStatus }
                if let queueHistorical { self.queueHistorical = queueHistorical }
                if let evidence { self.evidence = evidence }
                if let queueFailure { self.queueFailure = queueFailure }
                if let deliveryProblem { self.deliveryProblem = deliveryProblem }
            }
        }
        func recordAction(_ request: URLRequest) {
            guard let body = request.httpBody,
                  let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
                  let action = object["action"] as? String else { return }
            lock.withLock { actionHistory.append(action) }
        }
        func configureCatalog(epoch: String, sequence: UInt64) { lock.withLock { catalogEpoch = epoch; catalogSequence = sequence } }
        func recordFreshSnapshot() { lock.withLock { freshSnapshots += 1 } }
        func response(path: String, olderPage: Bool = false) -> (Int, String) {
            lock.withLock {
                switch path {
                case "/api/v1/session":
                    reads += 1
                    var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as! [String: Any]
                    if olderPage { object["journalSeq"] = 1 }
                    return (200, String(data: try! JSONSerialization.data(withJSONObject: object), encoding: .utf8)!)
                case "/api/v1/snapshot":
                    var snapshot = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
                    snapshot["daemon"] = ["running": true, "stale": stale, "refreshError": "Agent catalog refresh failed"]
                    if let catalogEpoch { snapshot["hostEpoch"] = catalogEpoch }
                    if let catalogSequence { snapshot["catalogSeq"] = catalogSequence }
                    if deliveryProblem {
                        snapshot["deliveryProblems"] = [["deliveryId": 42, "sessionId": "demo", "sourceSessionId": "source", "message": "Keep the regression test", "reason": "source unavailable", "status": "failed", "at": "2026-10-04T03:00:00Z"]]
                    }
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
        Self.state.recordAction(request)
        let olderPage = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.contains { $0.name == "timelineBefore" } == true
        if request.url?.path == "/api/v1/snapshot",
           URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.contains(where: { $0.name == "fresh" && $0.value == "1" }) == true {
            Self.state.recordFreshSnapshot()
        }
        let (status, body) = Self.state.response(path: request.url!.path, olderPage: olderPage)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
