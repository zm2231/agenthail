import XCTest
@testable import Agenthail

final class SessionExperienceTests: XCTestCase {
    func testWorkspaceHierarchyNestsComponentAncestorsAndKeepsFullPaths() {
        let groups = WorkspaceHierarchy.groups(for: [
            "/workspace/repos/agenthail",
            "/workspace/repos/companion",
            "/workspace/repos",
            "/workspace/repos/agenthail/native",
            "/workspace-ish/unrelated",
            "/workspace/repos/agenthail",
        ])
        XCTAssertEqual(groups, [
            WorkspaceGroup(path: "/workspace/repos", depth: 0),
            WorkspaceGroup(path: "/workspace/repos/agenthail", depth: 1),
            WorkspaceGroup(path: "/workspace/repos/agenthail/native", depth: 2),
            WorkspaceGroup(path: "/workspace/repos/companion", depth: 1),
            WorkspaceGroup(path: "/workspace-ish/unrelated", depth: 0),
        ])
    }

    func testWorkspaceHierarchyKeepsSessionsWithoutOrUnnormalizedWorkspaces() {
        let groups = WorkspaceHierarchy.groups(for: ["", "/work/app/", "/work/app/../app", "/work/app"])
        XCTAssertEqual(groups, [
            WorkspaceGroup(path: "", depth: 0),
            WorkspaceGroup(path: "/work/app", depth: 0),
        ])
        XCTAssertEqual(WorkspaceHierarchy.normalize(""), "")
        XCTAssertEqual(WorkspaceHierarchy.normalize("/work/app/"), "/work/app")
        XCTAssertEqual(WorkspaceHierarchy.normalize("/work/app/../app"), "/work/app")
    }

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
        XCTAssertEqual(model.deliveryStatus["A"], "Queued for A; sends when current turn ends.")
        await model.loadSession("B")
        XCTAssertEqual(model.composer, "Only for B")
        model.send(to: session(id: "B", busy: false))
        XCTAssertEqual(SessionExperienceProtocol.state.actions.count, 1, "read-only detail must override writable list row")
    }

    @MainActor
    func testSavedMessagesShowWhenActivityTimelineIsEmpty() {
        let empty = SessionTimeline(nextBefore: nil, items: [], source: nil, truncated: false, unavailableReason: nil)
        XCTAssertFalse(SessionScreen.showsActivity(timeline: empty, itemCount: 0, activityCursor: nil, exchangeCount: 1))
        XCTAssertTrue(SessionScreen.showsActivity(timeline: empty, itemCount: 0, activityCursor: nil, exchangeCount: 0))
        XCTAssertTrue(SessionScreen.showsActivity(timeline: empty, itemCount: 0, activityCursor: 40, exchangeCount: 1))
        XCTAssertTrue(SessionScreen.showsActivity(timeline: empty, itemCount: 2, activityCursor: nil, exchangeCount: 1))
        let unavailable = SessionTimeline(nextBefore: nil, items: [], source: nil, truncated: false, unavailableReason: "Transcript unavailable")
        XCTAssertFalse(SessionScreen.showsActivity(timeline: unavailable, itemCount: 2, activityCursor: nil, exchangeCount: 0))
    }

    @MainActor
    func testModelSearchMatchesIDNameAndDescription() {
        let options = [
            ModelOption(id: "gpt-web-pro", displayName: "ChatGPT Web — Pro", description: nil, default: nil, allowsCustom: nil, supportedReasoningEfforts: nil, defaultReasoningEffort: nil),
            ModelOption(id: "opus", displayName: "Opus", description: "Deep reasoning", default: nil, allowsCustom: true, supportedReasoningEfforts: nil, defaultReasoningEffort: nil),
        ]
        XCTAssertEqual(SearchableModelSelectionSheet.matching(options, query: "  ").map(\.id), ["gpt-web-pro", "opus"])
        XCTAssertEqual(SearchableModelSelectionSheet.matching(options, query: "chatgpt web").map(\.id), ["gpt-web-pro"])
        XCTAssertEqual(SearchableModelSelectionSheet.matching(options, query: "OPUS").map(\.id), ["opus"])
        XCTAssertEqual(SearchableModelSelectionSheet.matching(options, query: "reasoning").map(\.id), ["opus"])
        XCTAssertTrue(SearchableModelSelectionSheet.matching(options, query: "missing").isEmpty)
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
    func testSessionMetadataLoadsAfterJournalWithoutBlockingThePage() async throws {
        SessionExperienceProtocol.state.reset()
        let model = makeModel()
        await model.loadSession("metadata")
        XCTAssertFalse(model.loadingSession)
        XCTAssertEqual(model.selectedDetail?.session.id, "metadata")
        for _ in 0..<100 where model.selectedDetail?.model != "Metadata model" {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertEqual(model.selectedDetail?.model, "Metadata model")
        XCTAssertEqual(model.selectedDetail?.goal?.status, "active")
        XCTAssertEqual(model.selectedDetail?.models?.first?.id, "metadata-model")
        XCTAssertEqual(model.selectedDetail?.claudeRuns?.first?.jobId, "job-1")
        XCTAssertEqual(model.selectedDetail?.claudeRuns?.first?.providerState, "working")
        XCTAssertEqual(model.selectedDetail?.claudeSubagents?.first?.agentId, "agent-1")
        XCTAssertEqual(model.selectedDetail?.claudeSubagents?.first?.transcriptPath, "/tmp/agent-1.jsonl")
        XCTAssertEqual(model.selectedDetail?.metadataErrors?["context"], "fixture warning")
    }

    @MainActor
    func testSessionMetadataFailureDoesNotHideJournal() async throws {
        let model = makeModel()
        await model.loadSession("metadata-fails")
        XCTAssertFalse(model.loadingSession)
        XCTAssertEqual(model.selectedDetail?.session.id, "metadata-fails")
        XCTAssertNil(model.sessionError)
    }

    @MainActor
    func testSessionMetadataErrorClearsPreviouslyAppliedClaudeEvidence() async throws {
        SessionExperienceProtocol.state.reset()
        let model = makeModel()
        await model.loadSession("metadata")
        try await waitUntil { model.selectedDetail?.claudeRuns?.isEmpty == false }
        SessionExperienceProtocol.state.failNextMetadata()
        await model.loadSession("metadata")
        try await waitUntil { SessionExperienceProtocol.state.metadataRequests.count >= 2 }
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertNil(model.selectedDetail?.claudeRuns)
        XCTAssertNil(model.selectedDetail?.claudeSubagents)
        XCTAssertNil(model.selectedDetail?.metadataErrors)
        XCTAssertEqual(model.selectedDetail?.session.id, "metadata")
    }

    @MainActor
    func testSessionMetadataClearsOmittedClaudeEvidenceAndRejectsLatePreviousSession() async throws {
        SessionExperienceProtocol.state.reset()
        let model = makeModel()
        await model.loadSession("metadata")
        try await waitUntil { model.selectedDetail?.claudeRuns?.isEmpty == false }

        await model.loadSession("metadata-slow")
        try await waitUntil { SessionExperienceProtocol.state.metadataRequests.contains("metadata-slow") }
        await model.loadSession("metadata-empty")
        try await waitUntil { model.selectedDetail?.session.id == "metadata-empty" && SessionExperienceProtocol.state.metadataRequests.contains("metadata-empty") }
        try await Task.sleep(for: .milliseconds(250))

        XCTAssertEqual(model.selectedDetail?.session.id, "metadata-empty")
        XCTAssertNil(model.selectedDetail?.claudeRuns)
        XCTAssertNil(model.selectedDetail?.claudeSubagents)
        XCTAssertNil(model.selectedDetail?.metadataErrors)
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

    @MainActor
    private func waitUntil(_ condition: @escaping () -> Bool) async throws {
        for _ in 0..<100 where !condition() { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(condition(), "Timed out waiting for metadata fixture")
    }
}

private final class SessionExperienceState: @unchecked Sendable {
    private let lock = NSLock()
    private var values: [[String: Any]] = []
    private var metadataIDs: [String] = []
    private var failNextMetadataRequest = false
    var actions: [[String: Any]] { lock.withLock { values } }
    var metadataRequests: [String] { lock.withLock { metadataIDs } }
    func append(_ body: [String: Any]) { lock.withLock { values.append(body) } }
    func recordMetadata(_ id: String) { lock.withLock { metadataIDs.append(id) } }
    func failNextMetadata() { lock.withLock { failNextMetadataRequest = true } }
    func consumeMetadataFailure() -> Bool { lock.withLock { defer { failNextMetadataRequest = false }; return failNextMetadataRequest } }
    func shouldFailMetadata() -> Bool { lock.withLock { failNextMetadataRequest } }
    func reset() { lock.withLock { values = []; metadataIDs = []; failNextMetadataRequest = false } }
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
                if id == "metadata" || id == "metadata-fails" || id == "metadata-empty" || id == "metadata-slow" {
                    object["context"] = NSNull(); object["goal"] = NSNull(); object["model"] = NSNull(); object["models"] = NSNull()
                }
                if id == "metadata", Self.state.shouldFailMetadata() {
                    object["claudeRuns"] = [["recordPath": "/tmp/stale-job.json", "jobId": "stale-job"]]
                    object["claudeSubagents"] = [["parentSessionId": "metadata", "agentId": "stale-agent", "transcriptPath": "/tmp/stale-agent.jsonl"]]
                    object["metadataErrors"] = ["context": "stale warning"]
                }
                body = String(data: try! JSONSerialization.data(withJSONObject: object), encoding: .utf8)!
            }
        } else if request.url!.path == "/api/v1/session-metadata" {
            let id = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!.queryItems!.first { $0.name == "id" }!.value!
            Self.state.recordMetadata(id)
            if id == "metadata-fails" || (id == "metadata" && Self.state.consumeMetadataFailure()) {
                status = 503; body = #"{"error":{"message":"Metadata unavailable"}}"#
            } else if id == "metadata-empty" {
                body = #"{"sessionId":"metadata-empty"}"#
            } else if id == "metadata-slow" {
                Thread.sleep(forTimeInterval: 0.2)
                body = #"{"sessionId":"metadata-slow","claudeRuns":[{"recordPath":"/tmp/slow-job.json","jobId":"slow-job","sessionId":"metadata-slow","providerState":"working"}],"claudeSubagents":[{"parentSessionId":"metadata-slow","agentId":"slow-agent","transcriptPath":"/tmp/slow-agent.jsonl"}]}"#
            } else {
                body = #"{"context":{"usedTokens":10,"contextWindow":100,"compacting":false,"compactionCount":0},"goal":{"objective":"Metadata goal","status":"active"},"model":"Metadata model","models":[{"id":"metadata-model","displayName":"Metadata model"}],"claudeRuns":[{"recordPath":"/tmp/job-1.json","jobId":"job-1","sessionId":"metadata","resumeSessionId":"resume-1","runType":"bg","providerState":"working","createdAt":"2026-10-04T00:00:00Z","updatedAt":"2026-10-04T00:01:00Z"}],"claudeSubagents":[{"parentSessionId":"metadata","agentId":"agent-1","transcriptPath":"/tmp/agent-1.jsonl"}],"errors":{"context":"fixture warning"}}"#
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
            body = #"{"ok":true,"result":{"evidence":"queued","queueId":7,"detail":"Queued for A; sends when current turn ends."}}"#
        } else if request.url!.path == "/api/v1/search" {
            body = #"{"results":[{"session":{"id":"older","surface":"codex","name":"Old build","status":"idle","queueCount":0,"open":false,"current":false,"capabilities":{"send":false,"stream":false,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}},"snippet":"Saved"}],"remoteError":""}"#
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
