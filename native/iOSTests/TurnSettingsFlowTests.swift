import Foundation
import XCTest
@testable import Agenthail

final class TurnSettingsFlowTests: XCTestCase {
    @MainActor
    func testBusyDefaultSendCarriesTurnOptionsAndClearsDraft() async throws {
        let fixture = try makeFixture(status: "busy")
        TurnSettingsFlowProtocol.state.reset()
        let model = makeModel()
        model.selectedSessionID = fixture.session.id
        model.selectedDetail = fixture.detail
        model.setTurnSettings(TurnSettings(effort: "high", mode: .plan), for: fixture.session.id)
        model.composer = "Steer the active turn"

        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 1 }

        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["action"] as? String, "send")
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["effort"] as? String, "high")
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["mode"] as? String, "plan")
        XCTAssertEqual(model.turnSettings(for: fixture.session.id), TurnSettings())
    }

    @MainActor
    func testBusyDefaultSendsNormalInstruction() async throws {
        let fixture = try makeFixture(status: "busy")
        TurnSettingsFlowProtocol.state.reset()
        let model = makeModel()
        model.selectedSessionID = fixture.session.id
        model.selectedDetail = fixture.detail
        model.composer = "Queue this instruction"

        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 1 }

        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["action"] as? String, "send")
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["message"] as? String, "Queue this instruction")
    }

    func testExplicitSteerAPIPathOmitsTurnOptions() async throws {
        TurnSettingsFlowProtocol.state.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [TurnSettingsFlowProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration))

        _ = try await api.sendInstruction(action: "steer", sessionID: "codex-flow", message: "Steer the active turn", turnSettings: TurnSettings(effort: "high", mode: .plan), idempotencyKey: "stable-steer-key")
        _ = try await api.sendInstruction(action: "steer", sessionID: "codex-flow", message: "Steer the active turn", turnSettings: TurnSettings(effort: "high", mode: .plan), idempotencyKey: "stable-steer-key")

        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["action"] as? String, "steer")
        XCTAssertNil(TurnSettingsFlowProtocol.state.actions[0]["effort"])
        XCTAssertNil(TurnSettingsFlowProtocol.state.actions[0]["mode"])
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actionIdempotencyKeys, ["stable-steer-key", "stable-steer-key"])
    }

    func testActionGetsGeneratedKeyButReadsDoNot() async throws {
        TurnSettingsFlowProtocol.state.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [TurnSettingsFlowProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration))

        _ = try await api.sendInstruction(action: "send", sessionID: "codex-flow", message: "Send once")
        try await api.action("goal-set", sessionID: "codex-flow", message: "Set a goal")
        _ = try await api.queuedInstructions()

        let generated = try XCTUnwrap(TurnSettingsFlowProtocol.state.actionIdempotencyKeys.compactMap { $0 }.first)
        XCTAssertNotNil(UUID(uuidString: generated))
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actionIdempotencyKeys.count, 2)
        XCTAssertNotEqual(TurnSettingsFlowProtocol.state.actionIdempotencyKeys[0], TurnSettingsFlowProtocol.state.actionIdempotencyKeys[1])
        XCTAssertEqual(TurnSettingsFlowProtocol.state.nonActionIdempotencyKeys, [nil])
    }

    @MainActor
    func testNormalSendFailurePreservesDraft() async throws {
        let fixture = try makeFixture(status: "idle")
        TurnSettingsFlowProtocol.state.reset(failActions: true)
        let model = makeModel()
        model.selectedSessionID = fixture.session.id
        model.selectedDetail = fixture.detail
        let settings = TurnSettings(effort: "low", mode: .default)
        model.setTurnSettings(settings, for: fixture.session.id)
        model.composer = "Retry this normally"

        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 1 }

        XCTAssertEqual(model.turnSettings(for: fixture.session.id), settings)
        XCTAssertTrue(model.composer.contains("Retry this normally"))
    }

    @MainActor
    func testNewerDraftWrittenDuringRefreshIsNotCleared() async throws {
        let fixture = try makeFixture(status: "idle")
        TurnSettingsFlowProtocol.state.reset(blockSessionRefresh: true)
        defer { TurnSettingsFlowProtocol.state.releaseSessionRefresh() }
        let model = makeModel()
        model.selectedSessionID = fixture.session.id
        model.selectedDetail = fixture.detail
        let sent = TurnSettings(effort: "high", mode: .plan)
        let newer = TurnSettings(effort: "low", mode: .default)
        model.setTurnSettings(sent, for: fixture.session.id)
        model.composer = "Send with the first draft"

        model.send(to: fixture.session)
        try await waitUntil { TurnSettingsFlowProtocol.state.sessionRefreshStarted }
        model.setTurnSettings(newer, for: fixture.session.id)
        TurnSettingsFlowProtocol.state.releaseSessionRefresh()
        try await waitUntil { model.sendingSessionIDs.isEmpty }

        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["effort"] as? String, "high")
        XCTAssertEqual(model.turnSettings(for: fixture.session.id), newer)
    }

    @MainActor
    private func makeModel() -> AgenthailIOSModel {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [TurnSettingsFlowProtocol.self]
        return AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: configuration)))
    }

    private func makeFixture(status: String) throws -> (session: SessionState, detail: SessionDetail) {
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as? [String: Any])
        var session = try XCTUnwrap(object["session"] as? [String: Any])
        session["id"] = "codex-flow"
        session["surface"] = "codex"
        session["status"] = status
        object["session"] = session
        var capabilities = try XCTUnwrap(object["capabilities"] as? [String: Any])
        capabilities["send"] = true
        capabilities["steer"] = true
        object["capabilities"] = capabilities
        let detail = try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: object))
        let state = SessionState(id: detail.session.id, surface: detail.session.surface, name: detail.session.name, alias: detail.alias,
                                 status: detail.session.status, lastActive: nil, queueCount: 0, open: true, current: true,
                                 currentReason: nil, capabilities: detail.capabilities, readOnly: false,
                                 readOnlyReason: "", cwd: detail.session.cwd)
        return (state, detail)
    }

    @MainActor
    private func waitUntil(_ condition: @escaping () -> Bool) async throws {
        for _ in 0..<100 where !condition() { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(condition(), "Timed out waiting for URLProtocol flow")
    }
}

private final class TurnSettingsFlowProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let condition = NSCondition()
        private var records: [[String: Any]] = []
        private var failActions = false
        private var blockSessionRefresh = false
        private var refreshStarted = false
        private var sessionRefreshReleased = false
        private var actionKeys: [String?] = []
        private var nonActionKeys: [String?] = []

        var actions: [[String: Any]] {
            condition.lock(); defer { condition.unlock() }
            return records
        }
        var sessionRefreshStarted: Bool {
            condition.lock(); defer { condition.unlock() }
            return refreshStarted
        }
        var actionIdempotencyKeys: [String?] {
            condition.lock(); defer { condition.unlock() }
            return actionKeys
        }
        var nonActionIdempotencyKeys: [String?] {
            condition.lock(); defer { condition.unlock() }
            return nonActionKeys
        }

        func reset(failActions: Bool = false, blockSessionRefresh: Bool = false) {
            condition.lock()
            records = []; actionKeys = []; nonActionKeys = []; self.failActions = failActions; self.blockSessionRefresh = blockSessionRefresh
            refreshStarted = false; sessionRefreshReleased = false
            condition.broadcast(); condition.unlock()
        }

        func record(_ body: [String: Any], idempotencyKey: String?) -> (Int, String) {
            condition.lock(); records.append(body); actionKeys.append(idempotencyKey); condition.broadcast(); condition.unlock()
            if failActions { return (502, #"{"error":{"message":"unavailable"}}"#) }
            return (200, #"{"ok":true}"#)
        }
        func recordNonAction(idempotencyKey: String?) {
            condition.lock(); nonActionKeys.append(idempotencyKey); condition.unlock()
        }

        func beginSessionRefresh() {
            condition.lock()
            refreshStarted = true; condition.broadcast()
            while blockSessionRefresh && !sessionRefreshReleased { condition.wait() }
            condition.unlock()
        }
        func releaseSessionRefresh() {
            condition.lock(); sessionRefreshReleased = true; condition.broadcast(); condition.unlock()
        }
    }

    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var status = 200
        var body = #"{"ok":true}"#
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
            let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
            (status, body) = Self.state.record(object, idempotencyKey: request.value(forHTTPHeaderField: "Idempotency-Key"))
        } else if request.url?.path == "/api/v1/queue" {
            Self.state.recordNonAction(idempotencyKey: request.value(forHTTPHeaderField: "Idempotency-Key"))
            body = #"{"items":[]}"#
        } else if request.url?.path == "/api/v1/session" {
            Self.state.beginSessionRefresh()
            status = 200
            body = SessionPreview.detailJSON
        } else {
            status = 503
            body = #"{"error":{"message":"unexpected fixture request"}}"#
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
