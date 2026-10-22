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
    func testSnapshotStatusReachesOpenSession() throws {
        let model = makeModel()
        model.selectedSessionID = "demo"
        model.selectedDetail = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.detailJSON.utf8))
        XCTAssertEqual(model.selectedDetail?.session.status, "busy")
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as? [String: Any])
        var sessions = try XCTUnwrap(object["sessions"] as? [[String: Any]])
        let index = try XCTUnwrap(sessions.firstIndex { $0["id"] as? String == "demo" })
        sessions[index]["status"] = "idle"
        object["sessions"] = sessions
        model.snapshot = try JSONDecoder().decode(DashboardSnapshot.self, from: JSONSerialization.data(withJSONObject: object))

        XCTAssertEqual(model.selectedDetail?.session.status, "idle")
        XCTAssertEqual(model.selectedDetail?.model, "Claude Sonnet")
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
        private let lock = NSLock()
        private var fetches = 0
        private var unsupported = false

        var detailFetches: Int { lock.withLock { fetches } }
        var streamUnsupported: Bool { lock.withLock { unsupported } }
        func reset(streamUnsupported: Bool) { lock.withLock { fetches = 0; unsupported = streamUnsupported } }
        func fetched() { lock.withLock { fetches += 1 } }
    }

    static let state = State()
    static let detail: String = {
        var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as! [String: Any]
        for key in ["context", "goal", "model", "models"] { object.removeValue(forKey: key) }
        return String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self)
    }()
    static let metadata = #"{"model":"Claude Opus","models":[{"id":"opus","displayName":"Claude Opus"}],"context":{"usedTokens":1234,"contextWindow":200000,"compacting":false,"compactionCount":0},"goal":{"objective":"Keep the metadata","status":"active"}}"#

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var status = 200
        var body = #"{"items":[]}"#
        switch request.url?.path {
        case "/api/v1/session":
            Self.state.fetched()
            body = Self.detail
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
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
