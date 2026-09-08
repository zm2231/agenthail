import XCTest
@testable import Agenthail

final class WorkflowParityTests: XCTestCase {
    @MainActor
    func testClaudeCreationPreservesSettingsAndNativeIdentity() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        let settings = ClaudeCreationSettings(name: "phone-task", worktree: "phone-work", agent: "reviewer", effort: "high", permissionMode: "plan")
        let created = await model.createSession(surface: "claude", message: "Build", cwd: "/project", model: "sonnet", claude: settings)
        XCTAssertTrue(created)
        XCTAssertEqual(model.requestedSessionID, "native-claude")
        XCTAssertEqual(model.deliveryStatus["native-claude"], "Background session registered. Waiting for activity.")
        let sent = ParityProtocol.state.actions[0]
        for (key,value) in settings.fields { XCTAssertEqual(sent[key] as? String, value) }
        XCTAssertNil(sent["approvalPolicy"])
        _ = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "", claude: settings)
        for key in settings.fields.keys { XCTAssertNil(ParityProtocol.state.actions[1][key], "Claude settings must not leak after switching runtime") }
    }

    @MainActor
    func testClaudeUnknownCreationWithoutIdentityDoesNotNavigateOrRetry() async throws {
        ParityProtocol.state.reset(unknown: true)
        let model = makeModel()
        let created = await model.createSession(surface: "claude", message: "Build", cwd: "/project", model: "")
        XCTAssertFalse(created); XCTAssertNil(model.requestedSessionID)
        XCTAssertTrue(model.creationError?.contains("without a confirmed session ID") == true)
        XCTAssertTrue(model.creationError?.contains("registration delayed") == true)
        XCTAssertEqual(ParityProtocol.state.actions.count, 1)
    }

    func testQueueDecodesIntegratedTurnSettings() throws {
        let data = Data(#"{"id":7,"sessionId":"demo","sourceSessionId":"sender","target":"demo","message":"next","status":"pending","attempts":0,"queuedAt":"now","effort":"high","mode":"plan","serviceTier":"fast","outputSchema":{"type":"object","required":["answer"],"additionalProperties":false}}"#.utf8)
        let queue = try JSONDecoder().decode(QueueState.self, from: data)
        XCTAssertEqual(queue.sourceSessionId, "sender"); XCTAssertEqual(queue.effort, "high")
        XCTAssertEqual(queue.mode, "plan"); XCTAssertEqual(queue.serviceTier, "fast")
        let schema = try JSONSerialization.jsonObject(with: Data(queue.outputSchema!.formatted.utf8)) as! [String:Any]
        XCTAssertEqual(schema["required"] as? [String], ["answer"])
        XCTAssertEqual(schema["additionalProperties"] as? Bool, false)
    }
    func testCompactGroupingPreservesOrderErrorsAndStableAnchor() throws {
        let data = #"[{"id":"u","kind":"message","role":"user","title":"You","text":"work","truncated":false},{"id":"c","kind":"toolCall","title":"Bash","text":"{\"cmd\":\"swift test\"}","callId":"a","truncated":false},{"id":"r","kind":"toolResult","title":"Result","text":"failed","status":"error","callId":"a","truncated":true},{"id":"m","kind":"message","role":"assistant","title":"Assistant","text":"result","truncated":false}]"#
        let items = try JSONDecoder().decode([TimelineItem].self, from: Data(data.utf8))
        let groups = TimelineGroup.make(items)
        XCTAssertEqual(groups.count, 3)
        XCTAssertEqual(groups.flatMap(\.items).map(\.id), items.map(\.id))
        XCTAssertEqual(groups[1].callCount, 1); XCTAssertEqual(groups[1].errorCount, 1)
        XCTAssertEqual(groups[1].summary, "swift test")
        XCTAssertEqual(TimelineGroup.make(Array(items.prefix(2)))[1].id, groups[1].id)
        let boundary = Array(repeating: items[1], count: 12) + [items[2]]
        XCTAssertEqual(TimelineGroup.make(boundary).map { $0.items.count }, [13], "A call and result must not split at an arbitrary record count")
    }

    @MainActor
    func testCreationNavigatesKnownSessionAndPreservesUnknownReceipt() async throws {
        ParityProtocol.state.reset(unknown: true)
        let model = makeModel()
        let options = try await model.sessionOptions()
        XCTAssertEqual(options.surfaces.first?.id, "codex")
        XCTAssertEqual(options.workspaces, ["/project"])
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "/project", model: "chosen")
        XCTAssertTrue(created)
        XCTAssertEqual(model.requestedSessionID, "created")
        XCTAssertTrue(model.deliveryStatus["created"]!.contains("unconfirmed"))
        XCTAssertEqual(ParityProtocol.state.actions.first?["cwd"] as? String, "/project")
        XCTAssertEqual(ParityProtocol.state.actions.first?["model"] as? String, "chosen")
        model.creatingSession = true
        let duplicate = await model.createSession(surface: "codex", message: "Build", cwd: "", model: "")
        XCTAssertFalse(duplicate); XCTAssertEqual(ParityProtocol.state.actions.count, 1)
    }

    @MainActor
    func testCreationFailureKeepsRouteAndNotionUsesExistingContract() async throws {
        ParityProtocol.state.reset(fail: true)
        let model = makeModel()
        let created = await model.createSession(surface: "codex", message: "Build", cwd: "", model: "")
        XCTAssertFalse(created); XCTAssertNil(model.requestedSessionID); XCTAssertNotNil(model.creationError)
        XCTAssertFalse(model.creatingSession)
        ParityProtocol.state.reset()
        let notion = await model.createSession(surface: "notion", message: "Research", cwd: "", model: "")
        XCTAssertTrue(notion)
        XCTAssertEqual(ParityProtocol.state.actions.first?["action"] as? String, "notion-create")
    }

    @MainActor
    func testGoalAliasAndQueueSendExactOperations() async throws {
        ParityProtocol.state.reset()
        let model = makeModel()
        model.selectedDetail = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.detailJSON.utf8))
        try await model.editSession(id: "demo", action: "goal-set", text: "Verify release")
        try await model.editSession(id: "demo", action: "alias", text: "release")
        let queue = try JSONDecoder().decode(QueueState.self, from: Data(#"{"id":7,"sessionId":"demo","target":"demo","message":"next","status":"dead","attempts":1,"queuedAt":"now"}"#.utf8))
        try await model.updateQueue(queue, retry: true)
        try await model.updateQueue(queue, retry: false)
        let actions = ParityProtocol.state.actions
        XCTAssertEqual(actions.compactMap { $0["action"] as? String }, ["goal-set","alias","queue-retry","queue-cancel"])
        XCTAssertEqual(actions[0]["message"] as? String, "Verify release")
        XCTAssertEqual(actions[1]["alias"] as? String, "release")
        XCTAssertEqual(actions[2]["queueId"] as? Int, 7)
        XCTAssertTrue(model.pendingControls.isEmpty)
    }

    @MainActor private func makeModel() -> AgenthailIOSModel {
        let config = URLSessionConfiguration.ephemeral; config.protocolClasses = [ParityProtocol.self]
        return AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string:"https://fixture.invalid")!, token:"fixture", session: URLSession(configuration: config)))
    }
}

private final class ParityProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var records: [[String:Any]] = []
        private var unknown = false
        private var fail = false
        var actions: [[String:Any]] { lock.withLock { records } }
        func reset(unknown: Bool = false, fail: Bool = false) { lock.withLock { records = []; self.unknown = unknown; self.fail = fail } }
        func respond(_ body: [String:Any]) -> (Int,String) { lock.withLock {
            records.append(body)
            if fail { return (502,#"{"error":{"message":"unavailable"}}"#) }
            if (body["action"] as? String)?.contains("create") == true {
                if body["surface"] as? String == "claude" {
                    return unknown ? (202, #"{"ok":false,"unknown":true,"session":null,"error":"registration delayed"}"#) : (201, #"{"ok":true,"session":{"id":"native-claude","surface":"claude","name":"phone-task","status":"unknown","lastActive":"2026-09-08T08:00:00Z"},"result":null}"#)
                }
                return (unknown ? 202 : 201, unknown ? #"{"ok":false,"unknown":true,"sessionId":"created"}"# : #"{"ok":true,"sessionId":"created"}"#)
            }
            return (200,#"{"ok":true}"#)
        } }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        var status = 200
        var text = #"{"surfaces":[{"id":"codex","workspace":true}],"workspaces":["/project"]}"#
        if request.url?.path == "/api/v1/actions" {
            var data = request.httpBody ?? Data()
            if let stream = request.httpBodyStream { stream.open(); defer { stream.close() }; var buffer = [UInt8](repeating:0,count:4096); while stream.hasBytesAvailable { let n = stream.read(&buffer,maxLength:buffer.count); if n <= 0 { break }; data.append(contentsOf:buffer.prefix(n)) } }
            let body = (try? JSONSerialization.jsonObject(with:data)) as? [String:Any] ?? [:]
            (status,text) = Self.state.respond(body)
        } else if request.url?.path != "/api/v1/session-options" { status = 503; text = #"{"error":{"message":"fixture refresh unavailable"}}"# }
        client?.urlProtocol(self,didReceive:HTTPURLResponse(url:request.url!,statusCode:status,httpVersion:nil,headerFields:["Content-Type":"application/json"])!,cacheStoragePolicy:.notAllowed)
        client?.urlProtocol(self,didLoad:Data(text.utf8)); client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
