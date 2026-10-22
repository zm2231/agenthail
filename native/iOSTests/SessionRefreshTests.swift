import Foundation
import XCTest
@testable import Agenthail

final class SessionRefreshTests: XCTestCase {
    @MainActor
    func testDetailRefreshKeepsMetadata() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        await model.loadSession("demo")
        try await waitUntil(timeout: 2) { model.selectedDetail?.model == "Claude Opus" }

        await model.refreshSession("demo")

        XCTAssertEqual(model.selectedDetail?.model, "Claude Opus")
        XCTAssertEqual(model.selectedDetail?.context?.usedTokens, 1234)
        XCTAssertEqual(model.selectedDetail?.goal?.objective, "Keep the metadata")
        XCTAssertEqual(model.selectedDetail?.models?.count, 1)
    }

    @MainActor
    func testSnapshotStatusReachesOpenSession() async {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "demo"
        await model.refreshSession("demo")
        XCTAssertEqual(model.selectedDetail?.session.status, "busy")

        SessionRefreshProtocol.state.set(snapshotStatus: "idle")
        await model.refresh()

        XCTAssertEqual(model.selectedDetail?.session.status, "idle")
    }

    @MainActor
    func testOlderSnapshotDoesNotOverwriteNewerDetail() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "demo"
        await model.refreshSession("demo")
        SessionRefreshProtocol.state.holdSnapshot()
        let snapshot = Task { await model.refresh() }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.snapshotHeld }

        SessionRefreshProtocol.state.set(detailStatus: "idle")
        await model.refreshSession("demo")
        SessionRefreshProtocol.state.releaseSnapshot()
        _ = await snapshot.value

        XCTAssertEqual(model.selectedDetail?.session.status, "idle")
    }

    @MainActor
    func testOlderSnapshotDoesNotOverwriteNewerMatchingSnapshot() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "demo"
        await model.refreshSession("demo")
        SessionRefreshProtocol.state.set(snapshotStatus: "idle")
        SessionRefreshProtocol.state.holdSnapshot()
        let older = Task { await model.refresh() }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.snapshotHeld }

        SessionRefreshProtocol.state.set(snapshotStatus: "busy")
        SessionRefreshProtocol.state.passNextSnapshot()
        await model.refresh()
        SessionRefreshProtocol.state.releaseSnapshot()
        _ = await older.value

        XCTAssertEqual(model.selectedDetail?.session.status, "busy")
    }

    @MainActor
    func testOlderDetailDoesNotOverwriteNewerSnapshot() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        model.selectedSessionID = "demo"
        await model.refreshSession("demo")
        SessionRefreshProtocol.state.holdDetail()
        let detail = Task { await model.refreshSession("demo") }
        try await waitUntil(timeout: 2) { SessionRefreshProtocol.state.detailHeld }

        SessionRefreshProtocol.state.set(snapshotStatus: "idle")
        await model.refresh()
        SessionRefreshProtocol.state.releaseDetail()
        await detail.value

        XCTAssertEqual(model.selectedDetail?.session.status, "idle")
    }

    @MainActor
    func testLiveStreamReplacesPolling() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: false)
        let model = makeModel()
        await model.loadSession("demo")
        try await Task.sleep(for: .seconds(5))
        XCTAssertEqual(SessionRefreshProtocol.state.detailFetches, 1)
        model.selectedSessionID = nil
    }

    @MainActor
    func testUnsupportedStreamFallsBackToPolling() async throws {
        SessionRefreshProtocol.state.reset(streamUnsupported: true)
        let model = makeModel()
        await model.loadSession("demo")
        try await waitUntil(timeout: 6) { SessionRefreshProtocol.state.detailFetches >= 2 }
        model.selectedSessionID = nil
    }

    @MainActor
    private func makeModel() -> AgenthailIOSModel {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [SessionRefreshProtocol.self]
        return AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration)))
    }

    @MainActor
    private func waitUntil(timeout: TimeInterval, _ condition: () -> Bool) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while !condition(), Date() < deadline { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertTrue(condition(), "Timed out")
    }
}

private final class SessionRefreshProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let condition = NSCondition()
        private var fetches = 0
        private var unsupported = false
        private var detail = "busy"
        private var snapshot = "busy"
        private var holdingSnapshot = false
        private var holdingDetail = false
        private var snapshotWaiting = false
        private var detailWaiting = false

        var detailFetches: Int { condition.withLock { fetches } }
        var streamUnsupported: Bool { condition.withLock { unsupported } }
        var snapshotHeld: Bool { condition.withLock { snapshotWaiting } }
        var detailHeld: Bool { condition.withLock { detailWaiting } }
        func reset(streamUnsupported: Bool) {
            condition.withLock {
                fetches = 0; unsupported = streamUnsupported; detail = "busy"; snapshot = "busy"
                holdingSnapshot = false; holdingDetail = false; snapshotWaiting = false; detailWaiting = false; passing = 0
                condition.broadcast()
            }
        }
        func set(detailStatus: String) { condition.withLock { detail = detailStatus } }
        func set(snapshotStatus: String) { condition.withLock { snapshot = snapshotStatus } }
        private var passing = 0
        func holdSnapshot() { condition.withLock { holdingSnapshot = true } }
        func passNextSnapshot() { condition.withLock { passing += 1 } }
        func holdDetail() { condition.withLock { holdingDetail = true } }
        func releaseSnapshot() { condition.withLock { holdingSnapshot = false; condition.broadcast() } }
        func releaseDetail() { condition.withLock { holdingDetail = false; condition.broadcast() } }

        func detailResponse() -> String {
            condition.lock(); defer { condition.unlock() }
            fetches += 1
            let status = detail
            if holdingDetail {
                detailWaiting = true
                while holdingDetail { condition.wait() }
                detailWaiting = false
            }
            return SessionRefreshProtocol.detail(status: status)
        }

        func snapshotResponse() -> String {
            condition.lock(); defer { condition.unlock() }
            let status = snapshot
            if passing > 0 {
                passing -= 1
            } else if holdingSnapshot {
                snapshotWaiting = true
                while holdingSnapshot { condition.wait() }
                snapshotWaiting = false
            }
            return SessionRefreshProtocol.snapshot(status: status)
        }
    }

    static let state = State()
    static func detail(status: String) -> String {
        var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as! [String: Any]
        for key in ["context", "goal", "model", "models"] { object.removeValue(forKey: key) }
        var session = object["session"] as! [String: Any]
        session["status"] = status
        object["session"] = session
        return String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self)
    }
    static func snapshot(status: String) -> String {
        var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
        var sessions = object["sessions"] as! [[String: Any]]
        if let index = sessions.firstIndex(where: { $0["id"] as? String == "demo" }) { sessions[index]["status"] = status }
        object["sessions"] = sessions
        return String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self)
    }
    static let metadata = #"{"model":"Claude Opus","models":[{"id":"opus","displayName":"Claude Opus"}],"context":{"usedTokens":1234,"contextWindow":200000,"compacting":false,"compactionCount":0},"goal":{"objective":"Keep the metadata","status":"active"}}"#

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var status = 200
        var body = #"{"items":[]}"#
        switch request.url?.path {
        case "/api/v1/session", "/api/v1/snapshot":
            let detail = request.url?.path == "/api/v1/session"
            DispatchQueue.global().async { [self] in
                finish(status: 200, body: detail ? Self.state.detailResponse() : Self.state.snapshotResponse())
            }
            return
        case "/api/v1/session-metadata":
            body = Self.metadata
        case "/api/v1/session-stream":
            if Self.state.streamUnsupported {
                status = 409
                body = #"{"error":{"code":"stream_unsupported","message":"no stream"}}"#
            } else {
                client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: Data(": open\n\n".utf8))
                return
            }
        default:
            break
        }
        finish(status: status, body: body)
    }

    private func finish(status: Int, body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
