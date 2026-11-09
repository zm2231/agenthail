import Foundation
import XCTest
@testable import Agenthail

final class TurnSettingsFlowTests: XCTestCase {
    @MainActor
    func testAChipChangeIsSessionStateAndTheNextSendCarriesNoSettings() async throws {
        let fixture = try makeFixture(status: "busy")
        TurnSettingsFlowProtocol.state.reset()
        let model = makeModel()
        model.selectedSessionID = fixture.session.id
        model.selectedDetail = fixture.detail

        await model.setTurnSetting(TurnSetting.effort, to: "high", sessionID: fixture.session.id)
        let change = TurnSettingsFlowProtocol.state.actions[0]
        XCTAssertEqual(change["action"] as? String, "turn-setting")
        XCTAssertEqual(change["sessionId"] as? String, "codex-flow")
        XCTAssertEqual(change["setting"] as? String, "effort")
        XCTAssertEqual(change["value"] as? String, "high")
        try await waitUntil { model.selectedDetail?.turnSettings?.setting(TurnSetting.effort)?.value == "high" }

        model.composer = "Use the new effort"
        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 2 }
        let send = TurnSettingsFlowProtocol.state.actions[1]
        XCTAssertEqual(send["action"] as? String, "send")
        XCTAssertNil(send["effort"])
        XCTAssertNil(send["mode"])
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
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["author"] as? String, "person")
    }

    func testExplicitSteerReplaysItsKey() async throws {
        TurnSettingsFlowProtocol.state.reset()
        let api = AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([TurnSettingsFlowProtocol.self]))

        _ = try await api.sendInstruction(action: "steer", sessionID: "codex-flow", message: "Steer the active turn", idempotencyKey: "stable-steer-key")
        _ = try await api.sendInstruction(action: "steer", sessionID: "codex-flow", message: "Steer the active turn", idempotencyKey: "stable-steer-key")

        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["action"] as? String, "steer")
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actionIdempotencyKeys, ["stable-steer-key", "stable-steer-key"])
    }

    func testActionCanReplayStableLogicalKey() async throws {
        TurnSettingsFlowProtocol.state.reset()
        let api = AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([TurnSettingsFlowProtocol.self]))

        try await api.action("send", sessionID: "codex-flow", message: "Retry this normally", idempotencyKey: "stable-send-key")
        try await api.action("send", sessionID: "codex-flow", message: "Retry this normally", idempotencyKey: "stable-send-key")

        XCTAssertEqual(TurnSettingsFlowProtocol.state.actionIdempotencyKeys, ["stable-send-key", "stable-send-key"])
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions.map { $0["sessionId"] as? String }, ["codex-flow", "codex-flow"])
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions.map { $0["message"] as? String }, ["Retry this normally", "Retry this normally"])
    }

    func testSendCarriesExplicitBusyDeliveryOnlyWhenChosen() async throws {
        TurnSettingsFlowProtocol.state.reset()
        let api = AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([TurnSettingsFlowProtocol.self]))

        _ = try await api.sendInstruction(action: "send", sessionID: "codex-flow", message: "Follow the default")
        _ = try await api.sendInstruction(action: "send", sessionID: "codex-flow", message: "Steer now", busyDelivery: "steer")

        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions.map { $0["action"] as? String }, ["send", "send"])
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions.map { $0["busyDelivery"] as? String }, [nil, "steer"])
    }

    func testActionGetsGeneratedKeyButReadsDoNot() async throws {
        TurnSettingsFlowProtocol.state.reset()
        let api = AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([TurnSettingsFlowProtocol.self]))

        _ = try await api.sendInstruction(action: "send", sessionID: "codex-flow", message: "Send once")
        try await api.action("goal-set", sessionID: "codex-flow", message: "Set a goal")
        _ = try await api.activeQueue()

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
        model.composer = "Retry this normally"

        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 1 }

        XCTAssertTrue(model.composer.contains("Retry this normally"))

        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 2 }
        XCTAssertNotEqual(TurnSettingsFlowProtocol.state.actionIdempotencyKeys[0], TurnSettingsFlowProtocol.state.actionIdempotencyKeys[1], "the daemon replays a refusal under its key, so a retry needs a new one")
        XCTAssertEqual(model.deliveryStatus[fixture.session.id], "Couldn’t complete request. Draft kept.")

        model.composer = "Edited retry"
        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 3 }
        XCTAssertNotEqual(TurnSettingsFlowProtocol.state.actionIdempotencyKeys[1], TurnSettingsFlowProtocol.state.actionIdempotencyKeys[2])
        XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[2]["message"] as? String, "Edited retry")
    }

    @MainActor
    func testClaudeControlsSendTheirValueAndShowTheRequestLinkedResult() async throws {
        let fixture = try makeFixture(status: "idle")
        for (action, value) in [("context", nil), ("status", nil), ("usage", nil), ("effort", "medium"), ("rename", "Parser worker")] as [(String, String?)] {
            TurnSettingsFlowProtocol.state.reset()
            let model = makeModel()
            model.action(action, session: fixture.session, message: value)
            try await waitUntil { model.pendingControls.isEmpty && TurnSettingsFlowProtocol.state.actions.count >= 1 }
            XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["action"] as? String, action)
            XCTAssertEqual(TurnSettingsFlowProtocol.state.actions[0]["message"] as? String, value)
            XCTAssertEqual(model.controlOutput[fixture.session.id], "Context Usage 41%\nRequest request-proof")
        }
    }

    @MainActor
    func testTransportAcceptedSteerIsShownAsASteer() async throws {
        let fixture = try makeFixture(status: "busy")
        TurnSettingsFlowProtocol.state.reset()
        let model = makeModel()
        model.selectedSessionID = fixture.session.id
        model.selectedDetail = fixture.detail
        model.composer = "Steer over Remote Control"

        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 1 }

        XCTAssertEqual(model.deliveryStatus[fixture.session.id], "Steer sent to fixture.")
    }

    @MainActor
    func testATransportAcceptedSendWithoutDetailReadsSent() async throws {
        let fixture = try makeFixture(status: "idle")
        TurnSettingsFlowProtocol.state.reset()
        let model = makeModel()
        model.selectedSessionID = fixture.session.id
        model.selectedDetail = fixture.detail
        model.composer = "Send over a peer transport"

        model.send(to: fixture.session)
        try await waitUntil { model.sendingSessionIDs.isEmpty && TurnSettingsFlowProtocol.state.actions.count == 1 }

        XCTAssertEqual(model.deliveryStatus[fixture.session.id], "Sent")
    }

    @MainActor
    private func makeModel() -> AgenthailIOSModel {
        AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([TurnSettingsFlowProtocol.self])))
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
        var detail = try JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: object))
        detail.session.id = "computer::codex-flow"
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
        private var effort = "medium"
        private var actionKeys: [String?] = []
        private var nonActionKeys: [String?] = []

        var actions: [[String: Any]] {
            condition.lock(); defer { condition.unlock() }
            return records
        }
        var metadataJSON: String {
            condition.lock(); defer { condition.unlock() }
            return #"{"sessionId":"codex-flow","turnSettings":[{"key":"effort","value":"\#(effort)","applies":"nextTurn","options":[{"id":"medium","label":"Medium"},{"id":"high","label":"High"}]}]}"#
        }
        var actionIdempotencyKeys: [String?] {
            condition.lock(); defer { condition.unlock() }
            return actionKeys
        }
        var nonActionIdempotencyKeys: [String?] {
            condition.lock(); defer { condition.unlock() }
            return nonActionKeys
        }

        func reset(failActions: Bool = false) {
            condition.lock()
            records = []; actionKeys = []; nonActionKeys = []; self.failActions = failActions; effort = "medium"
            condition.unlock()
        }

        func record(_ body: [String: Any], idempotencyKey: String?) -> (Int, String) {
            condition.lock()
            records.append(body); actionKeys.append(idempotencyKey)
            if body["action"] as? String == "turn-setting", body["setting"] as? String == "effort", let value = body["value"] as? String { effort = value }
            condition.unlock()
            if failActions { return (502, #"{"error":{"message":"unavailable"}}"#) }
            if ["context", "status", "usage", "effort", "rename"].contains(body["action"] as? String ?? "") {
                return (200, #"{"ok":true,"result":{"evidence":"reply_observed","requestId":"request-proof","output":"Context Usage 41%"}}"#)
            }
            if body["message"] as? String == "Steer over Remote Control" {
                return (200, #"{"ok":true,"result":{"evidence":"transport_accepted","status":"sent","requestId":"posted-steer","detail":"Steer sent to fixture."}}"#)
            }
            if body["message"] as? String == "Send over a peer transport" {
                return (200, #"{"ok":true,"result":{"evidence":"transport_accepted","status":"sent","requestId":"posted-send"}}"#)
            }
            return (200, #"{"ok":true}"#)
        }
        func recordNonAction(idempotencyKey: String?) {
            condition.lock(); nonActionKeys.append(idempotencyKey); condition.unlock()
        }
    }

    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() { served(answer) }

    private func answer() {
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
            body = SessionPreview.detailJSON
        } else if request.url?.path == "/api/v1/session-metadata" {
            body = Self.state.metadataJSON
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
