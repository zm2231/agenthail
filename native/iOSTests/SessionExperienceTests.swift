import XCTest
@testable import Agenthail

final class SessionExperienceTests: XCTestCase {
    func testRichSessionDecodesContextTimelineAndTools() throws {
        let detail = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.detailJSON.utf8))
        XCTAssertEqual(detail.timeline?.items.count, 5)
        XCTAssertEqual(detail.context?.cachedInputTokens, 60000)
        XCTAssertEqual(detail.timeline?.nextBefore, 2400)
        XCTAssertEqual(detail.session.cwd, "/Users/demo/projects/fieldnotes")
        guard case .command(let command, let path) = ToolPresentation(name: "Bash", text: detail.timeline!.items[2].text).content else { return XCTFail("command presentation missing") }
        XCTAssertEqual(command, "swift test --filter ReconnectTests")
        XCTAssertEqual(path, "/Users/demo/projects/fieldnotes")
    }

    func testToolPresentationKeepsEditsPlansAndUnknownInputs() {
        guard case .edit(let path, let before, let after) = ToolPresentation(name: "Edit", text: #"{"file_path":"app.swift","old_string":"old","new_string":"new"}"#).content else { return XCTFail("edit") }
        XCTAssertEqual(path, "app.swift"); XCTAssertEqual(before, "old"); XCTAssertEqual(after, "new")
        guard case .plan(let items) = ToolPresentation(name: "update_plan", text: #"{"plan":[{"step":"Verify","status":"in_progress"}]}"#).content else { return XCTFail("plan") }
        XCTAssertEqual(items.first?.0, "Verify")
        guard case .raw(let raw) = ToolPresentation(name: "custom", text: "a custom payload").content else { return XCTFail("raw") }
        XCTAssertEqual(raw, "a custom payload")
    }

    @MainActor
    func testDraftsAreScopedAndFreshCapabilitiesGateSending() async throws {
        SessionExperienceProtocol.state.reset()
        let model = makeModel()
        await model.loadSession("A")
        model.composer = "Only for A"
        await model.loadSession("B")
        XCTAssertEqual(model.composer, "")
        model.composer = "Only for B"
        await model.loadSession("A")
        XCTAssertEqual(model.composer, "Only for A")
        let stale = session(id: "A", busy: true)
        model.send(to: stale)
        for _ in 0..<100 where model.sendingSessionIDs.contains("A") { try await Task.sleep(for: .milliseconds(10)) }
        let calls = SessionExperienceProtocol.state.actions
        XCTAssertEqual(calls.count, 1)
        XCTAssertEqual(calls.first?["action"] as? String, "send")
        XCTAssertEqual(calls.first?["message"] as? String, "Only for A")
        XCTAssertEqual(model.deliveryStatus["A"], "Queued for the agent")
        await model.loadSession("B")
        XCTAssertEqual(model.composer, "Only for B")
        model.send(to: session(id: "B", busy: false))
        XCTAssertEqual(SessionExperienceProtocol.state.actions.count, 1, "read-only detail must override writable list row")
    }

    @MainActor
    func testFailedSessionAndSearchAreRecoverable() async throws {
        SessionExperienceProtocol.state.reset()
        let model = makeModel()
        await model.loadSession("missing")
        XCTAssertNotNil(model.sessionError)
        XCTAssertFalse(model.loadingSession)
        await model.loadSession("A")
        XCTAssertNil(model.sessionError)
        XCTAssertEqual(model.selectedDetail?.session.id, "A")
        await model.searchSessions("build")
        XCTAssertEqual(model.searchResults.first?.session.id, "older")
        XCTAssertFalse(model.searching)
        await model.searchSessions("")
        XCTAssertTrue(model.searchResults.isEmpty)
    }

    @MainActor
    private func makeModel() -> AgenthailIOSModel {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [SessionExperienceProtocol.self]
        return AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: URLSession(configuration: config)))
    }
    private func session(id: String, busy: Bool) -> SessionState {
        SessionState(id: id, surface: "claude", name: id, alias: nil, status: busy ? "busy" : "idle", lastActive: nil, queueCount: 0,
                     open: true, current: true, currentReason: nil, capabilities: Capabilities(send: true, steer: true), readOnly: false, readOnlyReason: nil)
    }
}

private final class SessionExperienceState: @unchecked Sendable {
    private let lock = NSLock()
    private var values: [[String: Any]] = []
    var actions: [[String: Any]] { lock.withLock { values } }
    func append(_ body: [String: Any]) { lock.withLock { values.append(body) } }
    func reset() { lock.withLock { values = [] } }
}

private final class SessionExperienceProtocol: URLProtocol, @unchecked Sendable {
    static let state = SessionExperienceState()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        var status = 200
        var body = "{}"
        if request.url!.path == "/api/v1/session" {
            let id = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!.queryItems!.first { $0.name == "id" }!.value!
            if id == "missing" { status = 404; body = #"{"error":{"message":"Session no longer exists"}}"# }
            else {
                var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as! [String: Any]
                var session = object["session"] as! [String: Any]
                session["id"] = id; session["status"] = "idle"
                object["session"] = session; object["readOnly"] = id == "B"
                body = String(data: try! JSONSerialization.data(withJSONObject: object), encoding: .utf8)!
            }
        } else if request.url!.path == "/api/v1/queue" {
            body = #"{"items":[{"id":7,"sessionId":"A","target":"A","message":"Only for A","status":"pending","evidence":"queued","attempts":0,"queuedAt":"2026-09-12 04:00:00"}]}"#
        } else if request.url!.path == "/api/v1/actions" {
            var data = request.httpBody ?? Data()
            if let stream = request.httpBodyStream {
                stream.open(); defer { stream.close() }
                var buffer = [UInt8](repeating: 0, count: 4096)
                while stream.hasBytesAvailable { let count = stream.read(&buffer, maxLength: buffer.count); if count <= 0 { break }; data.append(buffer, count: count) }
            }
            Self.state.append((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:])
            body = #"{"ok":true,"result":{"evidence":"queued","queueId":7}}"#
        } else if request.url!.path == "/api/v1/search" {
            body = #"{"results":[{"session":{"id":"older","surface":"codex","name":"Old build","status":"idle","queueCount":0,"open":false,"current":false,"capabilities":{"send":false,"stream":false,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}},"snippet":"Saved"}],"remoteError":""}"#
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
