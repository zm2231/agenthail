#if DEBUG
import SwiftUI

// Public demo data exercises the real session views without pairing or network access.
struct SessionPreview: View {
    @StateObject private var model: AgenthailIOSModel
    private let session: SessionState
    private let detail: SessionDetail

    init() {
        let decoder = JSONDecoder()
        let capture = SessionCapture.current
        let snapshot = try! decoder.decode(DashboardSnapshot.self, from: Data((capture?.snapshotJSON ?? Self.snapshotJSON).utf8))
        let capturedDetail = capture.flatMap { $0.detailJSON[$0.initialSessionID] }
        var detail = try! decoder.decode(SessionDetail.self, from: Data((capturedDetail ?? Self.previewDetailJSON).utf8))
        if ProcessInfo.processInfo.arguments.contains("--preview-app"),
           let metadata = try? decoder.decode(SessionMetadata.self, from: Data(Self.previewMetadataJSON.utf8)) {
            detail.claudeRuns = metadata.claudeRuns
            detail.claudeSubagents = metadata.claudeSubagents
        }
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [SessionPreviewProtocol.self]
        let model = AgenthailIOSModel(
            api: AgenthailAPI(baseURL: URL(string: "https://preview.invalid")!, token: "public-demo", session: URLSession(configuration: config)),
            paired: ProcessInfo.processInfo.arguments.contains("--preview-voice-entry")
        )
        model.setHostSnapshot(snapshot)
        detail.session.id = HostCatalog.qualify("computer", detail.session.id)
        self.detail = detail
        session = model.snapshot?.sessions.first(where: { $0.id == detail.session.id }) ?? SessionState(
            id: detail.session.id, surface: detail.session.surface, name: detail.session.name, alias: detail.alias,
            status: detail.session.status, lastActive: nil, queueCount: 0, open: false, current: false,
            currentReason: nil, capabilities: detail.capabilities, readOnly: detail.readOnly,
            readOnlyReason: detail.readOnlyReason, cwd: detail.session.cwd)
        model.selectedSessionID = session.id
        model.selectedDetail = detail
        if capture == nil { model.composer = "Keep the regression check with the fix." }
        if ProcessInfo.processInfo.arguments.contains("--preview-delivery") {
            model.deliveryStatus[session.id] = "Queued for the agent"
        }
        _model = StateObject(wrappedValue: model)
    }
    var body: some View {
        AppearanceScope(appearance: model.appearance) { previewBody }
            .onAppear {
                guard let config = Self.previewAppearance else { return }
                model.appearance.preview(config, wallpapers: Self.previewWallpapers(config))
            }
    }

    /// An `image:` backdrop is read from the path in the argument, which the
    /// harness places inside the app's own container.
    static func previewWallpapers(_ config: UIConfig) -> [WallpaperImage] {
        let screen = UIScreen.main.nativeBounds.size
        return config.imagePaths.compactMap { path in
            (try? Data(contentsOf: URL(fileURLWithPath: path))).flatMap { WallpaperImage.decode($0, id: path, maxPixels: Int(max(screen.width, screen.height))) }
        }
    }

    static var previewAppearance: UIConfig? {
        let arguments = ProcessInfo.processInfo.arguments
        let theme = arguments.firstIndex(of: "--preview-theme").flatMap { $0 + 1 < arguments.count ? arguments[$0 + 1] : nil }
        var values = ["theme": theme ?? "system"]
        if let index = arguments.firstIndex(of: "--preview-backdrop") {
            values["background"] = index + 1 < arguments.count ? arguments[index + 1] : "mesh: 3x3, #2e1972, #182c58, #0e5671, #2d1655, #386870, #2e3340, #591948, #8a3049, #682e2c"
        } else if theme == nil {
            return nil
        }
        guard let json = try? JSONSerialization.data(withJSONObject: ["rev": "", "problems": [], "chosen": [:], "overrides": [:], "values": values]) else { return nil }
        return try? JSONDecoder().decode(UIConfig.self, from: json)
    }

    @ViewBuilder private var previewBody: some View {
        if ProcessInfo.processInfo.arguments.contains("--preview-app") {
            MainTabs(model: model)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-new") {
            NewSessionSheet(model: model)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-inspector") {
            SessionInspector(model: model, session: session, initialDetail: detail)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-workspace") {
            ConversationFlow(model: model, initialSessionID: session.id)
        } else {
            NavigationStack { SessionScreen(model: model, session: session) }
        }
    }
    /// The question a waiting preview session asks in its terminal.
    nonisolated static let previewWaiting: [String: String] = ["kind": "question", "id": "preview-question"]

    /// With --preview-unreachable the session runs in the host and waits on an
    /// approval Agenthail does not hold, and another session is stopped by a
    /// usage limit.
    nonisolated static var previewUnreachable: [String: Any] { ["kind": "approval", "id": "preview-approval", "answerable": false] }
    nonisolated static var previewQuestions: [String: Any] { ["kind": "question", "id": "preview-questions", "answerable": true] }
    nonisolated static let previewQuestionsJSON = #"{"permissionMode":"","requests":[{"id":"preview-questions","kind":"question","tool":"AskUserQuestion","alwaysAllow":false,"createdAt":"2026-09-12T04:03:00Z","questions":[{"question":"The reconnect fix touches the session store and the stream reader. Which part should land first so the other can build on it?","header":"Landing order for the reconnect fix","multiSelect":false,"options":[{"label":"Session store first","description":"Keeps the saved identity stable before the reader changes"},{"label":"Stream reader first","description":"Smaller change that unblocks the failing regression test today"},{"label":"Both together","description":"One pull request, one review, one risk window"},{"label":"Neither yet","description":"Write the failing test first and decide after"}]},{"question":"Where should the regression check run?","header":"Checks","multiSelect":true,"options":[{"label":"Mac","description":"Run the suite on the desktop build"},{"label":"iPhone","description":"Run it on the shared simulator"},{"label":"Dashboard","description":"Run it in the browser harness"}]}]}]}"#
    nonisolated static let previewHostedLifecycle: [String: Bool] = ["status": true, "logs": true, "stop": true, "resume": true]
    nonisolated static let previewUsageLimit: [String: String] = ["resetsAt": "2026-09-12T18:20:00Z"]

    /// With --preview-waiting the detail reports the question too; with
    /// --preview-waiting-catalog-only only the catalog does. With
    /// --preview-claude-goal the session reports a blocked goal it sets and
    /// clears but cannot pause or budget.
    nonisolated static var previewDetailJSON: String {
        let arguments = ProcessInfo.processInfo.arguments
        guard arguments.contains("--preview-waiting") || arguments.contains("--preview-claude-goal") || arguments.contains("--preview-unreachable") || arguments.contains("--preview-questions") else { return basePreviewDetailJSON }
        var detail = try! JSONSerialization.jsonObject(with: Data(basePreviewDetailJSON.utf8)) as! [String: Any]
        if arguments.contains("--preview-questions") {
            var session = detail["session"] as! [String: Any]
            session["waitingOn"] = previewQuestions
            detail["session"] = session
            var capabilities = detail["capabilities"] as! [String: Any]
            capabilities["respond"] = true
            detail["capabilities"] = capabilities
        }
        if arguments.contains("--preview-waiting") {
            var session = detail["session"] as! [String: Any]
            session["waitingOn"] = previewWaiting
            detail["session"] = session
        }
        if arguments.contains("--preview-unreachable") {
            var session = detail["session"] as! [String: Any]
            session["waitingOn"] = previewUnreachable
            session["runtimeKind"] = "claude-host"
            detail["session"] = session
            var capabilities = detail["capabilities"] as! [String: Any]
            capabilities["respond"] = true
            capabilities["lifecycle"] = previewHostedLifecycle
            detail["capabilities"] = capabilities
        }
        if arguments.contains("--preview-claude-goal") {
            var capabilities = detail["capabilities"] as! [String: Any]
            capabilities["goal"] = false
            capabilities["goalSet"] = true
            capabilities["goalStatus"] = true
            detail["capabilities"] = capabilities
            detail["goal"] = ["objective": "Make the release build pass on a clean checkout", "status": "blocked", "timeUsedSeconds": 540, "tokensUsed": 1078, "tokenBudget": NSNull(), "reason": "The signing certificate is not installed on this machine, so the release build cannot pass here.", "createdAt": "2026-09-07T12:00:00Z", "updatedAt": "2026-09-07T12:09:00Z"]
        }
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }

    nonisolated static var basePreviewDetailJSON: String {
        let reading = ProcessInfo.processInfo.arguments.contains("--preview-reading")
        let longActivity = ProcessInfo.processInfo.arguments.contains("--preview-long-activity")
        guard ProcessInfo.processInfo.arguments.contains("--preview-app") || reading || longActivity else { return detailJSON }
        var detail = try! JSONSerialization.jsonObject(with: Data(detailJSON.utf8)) as! [String: Any]
        var timeline = detail["timeline"] as! [String: Any]
        var items = timeline["items"] as! [[String: Any]]
        if longActivity {
            // One run of many commands with recorded output, between two replies.
            let runs: [[String: Any]] = (1...30).flatMap { n -> [[String: Any]] in
                let output = (1...30).map { "ok \(n).\($0) fieldnotes/Sources/Reconnect\($0).swift compiled" }.joined(separator: "\n")
                return [
                    ["id": "run-\(n)", "kind": "toolCall", "title": "Bash", "text": "{\"command\":\"swift build --target Reconnect\(n)\"}", "callId": "run-\(n)", "truncated": false],
                    ["id": "run-\(n)-result", "kind": "toolResult", "title": "Bash result", "text": output, "callId": "run-\(n)", "truncated": false],
                ]
            }
            items.insert(contentsOf: runs, at: 4)
            items.append(["id": "after-run", "kind": "message", "role": "assistant", "title": "assistant", "text": "Every target builds after reconnecting.", "truncated": false])
            detail["timeline"] = timeline.merging(["items": items, "nextBefore": 0]) { $1 }
            return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
        }
        items.removeLast()
        items.append(["id": "5", "kind": "reasoning", "title": "Thinking", "text": "The regression test covers the reconnect boundary. The remaining check is whether the queued instruction retains its session identity.", "truncated": false])
        items.append(["id": "6", "kind": "message", "role": "assistant", "title": "assistant", "text": "## Reconnect is fixed\n\nThe pending build survives reconnecting. **All 8 regression tests pass.**\n\n- Preserved the session identity\n- Kept the queued instruction attached\n- Added coverage for interrupted delivery\n\n```swift\nawait session.restorePendingBuild()\n```\n\n| Check | Result |\n| --- | --- |\n| Reconnect | Passed |\n| Queue identity | Passed |", "timestamp": "2026-09-12T04:03:00Z", "truncated": false])
        items.append(["id": "7", "kind": "event", "title": "Turn duration", "text": "1m55s", "timestamp": "2026-09-12T04:03:00Z", "truncated": false])
        if reading {
            let instructions = "# AGENTS.md instructions for /Users/demo/projects/fieldnotes\n\n" + String(repeating: "## Repository guidance\n\nRead the current implementation before editing. Preserve the session identity and include meaningful regression coverage.\n\n", count: 30)
            items.insert(["id": "context", "kind": "instructions", "title": "Instructions", "text": instructions, "truncated": false], at: 0)
            items.insert(["id": "long-message", "kind": "message", "role": "user", "title": "user", "text": String(repeating: "The phone should show the actual work clearly, including commands, results and context. Keep the transcript readable while the agent works.\n\n", count: 14), "truncated": false], at: 1)
            let edit = ["file_path": "/Users/demo/projects/fieldnotes/Sources/Reconnect.swift", "old_string": "let session = restore()\nawait session.resume()", "new_string": "let session = restore(keeping: identity)\nawait session.resume()\nawait session.restorePendingBuild()"]
            items.insert(contentsOf: [
                ["id": "edit-1", "kind": "toolCall", "title": "Edit", "text": String(data: try! JSONSerialization.data(withJSONObject: edit, options: .sortedKeys), encoding: .utf8)!, "callId": "edit-1", "truncated": false],
                ["id": "edit-1-result", "kind": "toolResult", "title": "Edit result", "text": "The file has been updated.", "callId": "edit-1", "truncated": false],
            ], at: items.count - 3)
            items[items.count - 2]["text"] = "# The session stays readable while work continues\n\nI found the reconnect boundary. The saved instruction is present, and the agent has its original session identity.\n\n## What the checks show\n\nThe regression suite passes. You can expand the recorded command above to inspect its full input and result.\n\nThe latest update is visible above the composer."
        }
        timeline["items"] = items
        timeline["nextBefore"] = 0
        detail["timeline"] = timeline
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }

    nonisolated static var snapshotJSON: String {
        let capabilities: [String: Bool] = ["send": true, "stream": true, "reply": true, "goal": true, "goalSet": true, "goalStatus": true, "compact": true, "model": true, "interrupt": true, "steer": true, "attach": true]
        let sessions: [[String: Any]] = [
            ["id": "demo", "project": ["id": "project-fieldnotes", "displayName": "fieldnotes", "rootPath": "/Users/demo/projects/fieldnotes", "kind": "git"], "name": "Make the build reliable", "surface": "claude", "cwd": "/Users/demo/projects/fieldnotes", "status": "busy", "lastActive": "2026-09-12T04:03:00Z", "queueCount": 1, "open": true, "current": true, "capabilities": capabilities, "plan": ["done": 3, "total": 5, "current": "Running the regression suite"]],
            ["id": "codex-demo", "project": ["id": "project-fieldnotes", "displayName": "fieldnotes", "rootPath": "/Users/demo/projects/fieldnotes", "kind": "git"], "name": "Review the release pipeline", "surface": "codex", "cwd": "/Users/demo/projects/fieldnotes", "status": "idle", "lastActive": "2026-09-12T03:45:00Z", "queueCount": 0, "open": true, "current": true, "capabilities": capabilities],
            ["id": "saved-demo", "project": ["id": "project-agenthail", "displayName": "agenthail", "rootPath": "/Users/demo/projects/agenthail", "kind": "git"], "name": "Map the application architecture", "surface": "claude", "cwd": "/Users/demo/projects/agenthail", "status": "idle", "lastActive": "2026-09-11T13:00:00Z", "queueCount": 0, "open": false, "current": false, "capabilities": capabilities, "plan": ["done": 2, "total": 4]],
            ["id": "h0123456789ab::remote-demo", "project": ["id": "h0123456789ab::project-agenthail", "displayName": "agenthail", "rootPath": "/home/demo/work/agenthail", "kind": "git"], "host": ["id": "h0123456789ab", "name": "vps.example.ts.net", "state": "connected"], "name": "Prove the history is true", "surface": "codex", "cwd": "/home/demo/work/agenthail/.worktrees/history-truth", "status": "busy", "lastActive": "2026-09-12T04:01:00Z", "queueCount": 0, "open": true, "current": true, "capabilities": capabilities],
            ["id": "h0123456789ab::remote-demo-2", "project": ["id": "h0123456789ab::project-agenthail", "displayName": "agenthail", "rootPath": "/home/demo/work/agenthail", "kind": "git"], "host": ["id": "h0123456789ab", "name": "vps.example.ts.net", "state": "connected"], "name": "Tighten the release notes", "surface": "claude", "cwd": "/home/demo/work/agenthail/.worktrees/release-notes", "status": "idle", "lastActive": "2026-09-12T03:30:00Z", "queueCount": 0, "open": true, "current": true, "capabilities": capabilities, "claudeConfigRoot": ["name": "work", "root": "/home/dev/.work/.claude"]]
        ]
        var snapshot: [String: Any] = ["updatedAt": "2026-09-12T04:03:00Z", "daemon": ["running": true], "surfaces": [], "sessions": sessions, "totalSessions": sessions.count, "queue": [], "channels": [], "relays": [], "history": [], "attention": [], "codexRecentHours": 24]
        if ProcessInfo.processInfo.arguments.contains(where: { $0.hasPrefix("--preview-waiting") }) {
            snapshot["busyDelivery"] = "steer"
            var waitingSessions = sessions
            waitingSessions[0]["waitingOn"] = previewWaiting
            waitingSessions[0]["runtime"] = ["launcher": "cmux", "focusable": true]
            snapshot["sessions"] = waitingSessions
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-questions") {
            var asking = snapshot["sessions"] as! [[String: Any]]
            asking[0]["waitingOn"] = previewQuestions
            var askingCapabilities: [String: Any] = capabilities
            askingCapabilities["respond"] = true
            asking[0]["capabilities"] = askingCapabilities
            snapshot["sessions"] = asking
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-unreachable") {
            var limited = snapshot["sessions"] as! [[String: Any]]
            limited[0]["waitingOn"] = previewUnreachable
            limited[0]["runtimeKind"] = "claude-host"
            var hostedCapabilities: [String: Any] = capabilities
            hostedCapabilities["respond"] = true
            hostedCapabilities["lifecycle"] = previewHostedLifecycle
            limited[0]["capabilities"] = hostedCapabilities
            limited[4]["usageLimit"] = previewUsageLimit
            limited[4]["status"] = "idle"
            snapshot["sessions"] = limited
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-host-offline") {
            var offline = snapshot["sessions"] as! [[String: Any]]
            offline[4]["host"] = ["id": "h0123456789ab", "name": "vps.example.ts.net", "state": "unreachable"]
            offline[4]["unavailableReason"] = "vps.example.ts.net is offline"
            snapshot["sessions"] = offline
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-attention") {
            snapshot["attention"] = [["id": 1, "sessionId": "codex-demo", "target": "Review the release pipeline", "queueId": 2, "reason": "Delivery outcome could not be confirmed", "requestedAction": "review", "createdAt": "2026-09-11T18:20:00Z"]]
            snapshot["deliveryProblems"] = [["deliveryId": 7, "sessionId": "h0123456789ab::remote-demo-2", "sourceSessionId": "saved-demo", "attentionSessionId": "saved-demo", "message": "Check the release notes", "reason": "expired", "status": "expired", "at": "2026-09-11T13:05:00Z"]]
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-holders") {
            var sharedSessions = snapshot["sessions"] as! [[String: Any]]
            sharedSessions[0]["holders"] = [
                ["pid": 4101, "tty": "ttys001", "status": "busy", "startedAt": "2026-09-12T03:50:00Z"],
                ["pid": 4102, "tty": "ttys002", "status": "idle", "startedAt": "2026-09-12T03:58:00Z"]
            ]
            snapshot["sessions"] = sharedSessions
        }
        return String(data: try! JSONSerialization.data(withJSONObject: snapshot), encoding: .utf8)!
    }

    nonisolated static let queueJSON = #"{"items":[{"id":1,"sessionId":"demo","target":"Make the build reliable","message":"Keep the regression check with the fix.","status":"pending","evidence":"queued","attempts":0,"queuedAt":"2026-09-12T04:00:00.000000000Z"},{"id":2,"sessionId":"codex-demo","target":"Review the release pipeline","message":"Verify the signing step before the next release.","status":"dead","evidence":"unknown","attempts":1,"lastError":"Delivery outcome is unknown. Check the session before retrying.","queuedAt":"2026-09-11T18:20:00.000000000Z"},{"id":3,"sessionId":"saved-demo","target":"Map the application architecture","message":"Old release reminder","status":"canceled","evidence":"canceled","attempts":0,"queuedAt":"2026-09-09T11:00:00.000000000Z"}]}"#

    nonisolated static let previewMetadataJSON = #"{"sessionId":"demo","claudeRuns":[{"recordPath":"/Users/demo/.claude/runs/job-1.json","jobId":"job-1","sessionId":"demo","runType":"bg","providerState":"working","createdAt":"2026-09-07T12:00:00Z","updatedAt":"2026-09-07T12:04:00Z"}],"claudeSubagents":[{"parentSessionId":"demo","agentId":"agent-1","transcriptPath":"/Users/demo/.claude/agents/agent-1.jsonl"}]}"#

    nonisolated static let detailJSON = #"""
    {
      "session":{"id":"demo","surface":"claude","name":"Make the build reliable","status":"busy","lastActive":"2026-09-07T12:05:00Z","cwd":"/Users/demo/projects/fieldnotes","source":"cli","transport":"local"},
      "journalSeq":2048,
      "exchanges":[],"capabilities":{"send":true,"stream":true,"reply":true,"goal":true,"goalSet":true,"goalStatus":true,"compact":true,"model":true,"interrupt":true,"steer":true,"attach":true},
      "readOnly":false,"readOnlyReason":"","model":"Claude Sonnet",
      "models":[{"id":"sonnet","displayName":"Claude Sonnet"},{"id":"opus","displayName":"Claude Opus"}],
      "context":{"usedTokens":86400,"contextWindow":200000,"cumulativeTokens":312000,"compacting":false,"compactionCount":1,"reclaimedTokens":42000,"inputTokens":83000,"cachedInputTokens":60000,"outputTokens":3400},
      "goal":{"objective":"Fix the failing build and verify the release path","status":"active"},
      "timeline":{"source":"claude","truncated":false,"nextBefore":2400,"items":[
        {"id":"1","kind":"message","role":"user","title":"user","text":"The build fails after reconnecting. Trace it, fix it, and keep a regression test.","timestamp":"2026-09-07T12:00:00Z","truncated":false},
        {"id":"2","kind":"message","role":"assistant","title":"assistant · commentary","text":"The reconnect restores the conversation, but drops the pending build state. I’m checking the transition before changing it.","timestamp":"2026-09-07T12:01:00Z","truncated":false},
        {"id":"3","kind":"toolCall","title":"Bash","text":"{\"command\":\"swift test --filter ReconnectTests\",\"workdir\":\"/Users/demo/projects/fieldnotes\"}","callId":"build-1","timestamp":"2026-09-07T12:02:00Z","truncated":false},
        {"id":"4","kind":"toolResult","title":"Bash result","text":"Test Suite ReconnectTests passed.\nExecuted 8 tests, with 0 failures.","callId":"build-1","timestamp":"2026-09-07T12:03:00Z","truncated":false},
        {"id":"5","kind":"toolCall","title":"TodoWrite","text":"{\"todos\":[{\"content\":\"Trace reconnect state\",\"status\":\"completed\"},{\"content\":\"Verify the release build\",\"status\":\"in_progress\"}]}","timestamp":"2026-09-07T12:04:00Z","truncated":false}
      ]}
    }
    """#
}

private final class SessionPreviewProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    private let feed = PreviewStreamFeed()

    override func startLoading() {
        let streamed = SessionCapture.current?.initialSessionID ?? "demo"
        let requestQuery = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let requestID = requestQuery.first(where: { $0.name == "id" })?.value
        if PreviewStream.enabled, request.url?.path == "/api/v1/session", requestID == streamed,
           requestQuery.first(where: { $0.name == "timelineBefore" }) == nil {
            let base = SessionCapture.current?.detailJSON[streamed] ?? SessionPreview.previewDetailJSON.replacingOccurrences(of: "\"id\":\"demo\"", with: "\"id\":\"\(streamed)\"")
            let body = PreviewStream.detailJSON(base: base, captured: SessionCapture.current != nil)
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(body.utf8))
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        if PreviewStream.enabled, request.url?.path == "/api/v1/session-stream", requestID == streamed {
            let after = requestQuery.first(where: { $0.name == "after" })?.value.flatMap(UInt64.init) ?? 0
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
            feed.start(session: streamed, after: after, send: { [weak self] data in
                guard let self else { return }
                self.client?.urlProtocol(self, didLoad: data)
            }, drop: { [weak self] in
                guard let self else { return }
                self.client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
            })
            return
        }
        var status = 200
        let body: String
        if let capture = SessionCapture.current {
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
            let id = query.first(where: { $0.name == "id" })?.value ?? ""
            let captured: String?
            switch request.url?.path {
            case "/api/v1/snapshot":
                if let parentID = query.first(where: { $0.name == "parentId" })?.value { captured = query.contains { $0.name == "cursor" } ? nil : capture.subagentPageJSON[parentID] }
                else { captured = capture.snapshotJSON }
            case "/api/v1/queue": captured = capture.queueJSON
            case "/api/v1/session":
                let before = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "timelineBefore" })?.value
                captured = before == nil || before == "0" ? capture.detailJSON[id] : nil
            default: captured = nil
            }
            body = captured ?? #"{"error":{"message":"This local capture does not include this request or execute actions."}}"#
            status = captured == nil ? 503 : 200
        } else {
        switch request.url?.path {
        case "/api/v1/session-options": body = #"{"surfaces":[{"id":"claude","workspace":true},{"id":"codex","workspace":true},{"id":"notion","workspace":false}],"projects":[{"id":"p-fieldnotes","displayName":"fieldnotes","rootPath":"/Users/demo/projects/fieldnotes","kind":"git"}],"claudeConfigRoots":[{"name":"default","root":"/Users/demo/.claude","primary":true,"signedInAs":{"email":"demo@example.com"}},{"name":"team","root":"/Users/demo/.team/.claude","primary":false,"signedInAs":{"email":"team@example.com"}}]}"#
        case "/api/v1/session-options/claude-pick":
            body = #"{"pick":{"root":"/Users/demo/.team/.claude","name":"team","account":"team@example.com","reason":"most headroom, session 12%, week 30%"},"policy":{"auto":true,"sessionCeiling":90,"weeklyCeiling":90}}"#
        case "/api/v1/session-options/claude-usage":
            let root = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "root" })?.value ?? ""
            let (session, week) = root.hasSuffix(".team/.claude") ? (12, 30) : (40, 93)
            body = #"{"root":"\#(root)","usage":{"limits":[{"kind":"session","percent":\#(session)},{"kind":"weekly_all","percent":\#(week)}]}}"#
        case "/api/v1/models":
            body = #"{"models":[{"id":"default","displayName":"Runtime default","default":true},{"id":"gpt-5.6-sol","displayName":"GPT-5.6-Sol","description":"Latest frontier agentic coding model.","supportedReasoningEfforts":["low","medium","high"],"defaultReasoningEffort":"low"},{"id":"chatgpt-web/pro","displayName":"ChatGPT Web — Pro","supportedReasoningEfforts":["ultra"],"defaultReasoningEffort":"ultra"},{"id":"gpt-5.5","displayName":"GPT-5.5","supportedReasoningEfforts":["medium","high"],"defaultReasoningEffort":"medium"},{"id":"gpt-5.3-codex-spark","displayName":"GPT-5.3 Codex Spark","supportedReasoningEfforts":["low","medium"],"defaultReasoningEffort":"low"},{"id":"provider/custom-runtime","displayName":"Provider custom runtime","description":"Provider supplied model ID","allowsCustom":true}]}"#
        case "/api/v1/session-commands":
            body = #"{"commands":[{"name":"review","description":"Review the current changes","argumentHint":"","insert":"/review"},{"name":"compact","description":"Summarize the conversation so far","argumentHint":"[instructions]","insert":"/compact"},{"name":"fieldnotes","description":"Write up today's notes","argumentHint":"","insert":"$fieldnotes"}],"unavailable":""}"#
        case "/api/v1/session-files":
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "q" })?.value ?? ""
            let files = [("Sources", true), ("Sources/Notebook.swift", false), ("Sources/NoteStore.swift", false), ("README.md", false)]
                .filter { query.isEmpty || $0.0.localizedCaseInsensitiveContains(query) }
                .map { #"{"path":"\#($0.0)","dir":\#($0.1)}"# }
            body = #"{"files":[\#(files.joined(separator: ","))],"truncated":false}"#
        case "/api/v1/snapshot": body = SessionPreview.snapshotJSON
        case "/api/v1/queue": body = SessionPreview.queueJSON
        case "/api/v1/requests" where ProcessInfo.processInfo.arguments.contains("--preview-questions"): body = SessionPreview.previewQuestionsJSON
        case "/api/v1/usage": body = UsagePreview.json()
        case "/api/v1/session-metadata": body = #"{"sessionId":"demo","claudeRuns":[{"recordPath":"/Users/demo/.claude/runs/job-1.json","jobId":"job-1","sessionId":"demo","runType":"bg","providerState":"working","createdAt":"2026-09-07T12:00:00Z","updatedAt":"2026-09-07T12:04:00Z"}],"claudeSubagents":[{"parentSessionId":"demo","agentId":"agent-1","transcriptPath":"/Users/demo/.claude/agents/agent-1.jsonl"}]}"#
        case "/api/v1/session":
            let id = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "id" })?.value ?? "demo"
            body = SessionPreview.previewDetailJSON.replacingOccurrences(of: "\"id\":\"demo\"", with: "\"id\":\"\(id)\"")
        default: status = 503; body = #"{"error":{"message":"This public preview does not execute actions."}}"#
        }
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() { feed.stop() }
}
#endif
