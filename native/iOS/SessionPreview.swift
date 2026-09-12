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
        let detail = try! decoder.decode(SessionDetail.self, from: Data((capturedDetail ?? Self.previewDetailJSON).utf8))
        self.detail = detail
        session = snapshot.sessions.first(where: { $0.id == detail.session.id }) ?? SessionState(id: "demo", surface: "claude", name: "Make the build reliable", alias: nil, status: "busy", lastActive: nil,
                               queueCount: 1, open: true, current: true, currentReason: nil, capabilities: detail.capabilities, readOnly: false, readOnlyReason: nil)
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [SessionPreviewProtocol.self]
        let model = AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://preview.invalid")!, token: "public-demo", session: URLSession(configuration: config)))
        model.selectedSessionID = session.id
        model.selectedDetail = detail
        if capture == nil { model.composer = "Keep the regression check with the fix." }
        model.snapshot = snapshot
        if ProcessInfo.processInfo.arguments.contains("--preview-delivery") {
            model.deliveryStatus[session.id] = "Queued for the agent"
        }
        _model = StateObject(wrappedValue: model)
    }
    var body: some View {
        if ProcessInfo.processInfo.arguments.contains("--preview-app") {
            MainTabs(model: model)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-new") {
            NewSessionSheet(model: model)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-inspector") {
            SessionInspector(model: model, session: session, detail: detail)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-workspace") {
            ConversationFlow(model: model, initialSessionID: session.id).tint(SessionStyle.accent)
        } else {
            NavigationStack { SessionScreen(model: model, session: session) }.tint(.orange)
        }
    }
    nonisolated static var previewDetailJSON: String {
        guard ProcessInfo.processInfo.arguments.contains("--preview-app") || ProcessInfo.processInfo.arguments.contains("--preview-rich") else { return detailJSON }
        var detail = try! JSONSerialization.jsonObject(with: Data(detailJSON.utf8)) as! [String: Any]
        var timeline = detail["timeline"] as! [String: Any]
        var items = timeline["items"] as! [[String: Any]]
        items.removeLast()
        items.append(["id": "5", "kind": "reasoning", "title": "Thinking", "text": "The regression test covers the reconnect boundary. The remaining check is whether the queued instruction retains its session identity.", "truncated": false])
        items.append(["id": "6", "kind": "message", "role": "assistant", "title": "assistant", "text": "## Reconnect is fixed\n\nThe pending build survives reconnecting. **All 8 regression tests pass.**\n\n- Preserved the session identity\n- Kept the queued instruction attached\n- Added coverage for interrupted delivery\n\n```swift\nawait session.restorePendingBuild()\n```\n\n| Check | Result |\n| --- | --- |\n| Reconnect | Passed |\n| Queue identity | Passed |", "timestamp": "2026-09-12T04:03:00Z", "truncated": false])
        items.append(["id": "7", "kind": "event", "title": "Turn duration", "text": "1m55s", "timestamp": "2026-09-12T04:03:00Z", "truncated": false])
        timeline["items"] = items
        timeline["nextBefore"] = 0
        if ProcessInfo.processInfo.arguments.contains("--preview-history-only") {
            timeline["items"] = []
            detail["exchanges"] = [["user": "Read the saved conversation", "assistant": "Saved message history remains readable.", "timestamp": "2026-09-12T04:03:00Z"]]
        }
        detail["timeline"] = timeline
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }

    nonisolated static var snapshotJSON: String {
        let capabilities: [String: Bool] = ["send": true, "stream": true, "reply": true, "goal": true, "compact": true, "model": true, "interrupt": true, "steer": true]
        let sessions: [[String: Any]] = [
            ["id": "demo", "name": "Make the build reliable", "surface": "claude", "cwd": "/Users/demo/projects/fieldnotes", "status": "busy", "lastActive": "2026-09-12T04:03:00Z", "queueCount": 1, "open": true, "current": true, "capabilities": capabilities],
            ["id": "codex-demo", "name": "Review the release pipeline", "surface": "codex", "cwd": "/Users/demo/projects/fieldnotes", "status": "idle", "lastActive": "2026-09-12T03:45:00Z", "queueCount": 0, "open": true, "current": true, "capabilities": capabilities],
            ["id": "saved-demo", "name": "Map the application architecture", "surface": "claude", "cwd": "/Users/demo/projects/agenthail", "status": "idle", "lastActive": "2026-09-11T13:00:00Z", "queueCount": 0, "open": false, "current": false, "capabilities": capabilities]
        ]
        let snapshot: [String: Any] = ["updatedAt": "2026-09-12T04:03:00Z", "daemon": ["running": true], "surfaces": [], "sessions": sessions, "totalSessions": sessions.count, "queue": [], "channels": [], "relays": [], "history": [], "attention": [], "codexRecentHours": 24]
        return String(data: try! JSONSerialization.data(withJSONObject: snapshot), encoding: .utf8)!
    }

    nonisolated static let queueJSON = #"{"items":[{"id":1,"sessionId":"demo","target":"Make the build reliable","message":"Keep the regression check with the fix.","status":"pending","attempts":0,"queuedAt":"2026-09-12 04:00:00"},{"id":2,"sessionId":"codex-demo","target":"Review the release pipeline","message":"Verify the signing step before the next release.","status":"dead","attempts":1,"lastError":"Delivery outcome is unknown. Check the session before retrying.","queuedAt":"2026-09-11 18:20:00"},{"id":3,"sessionId":"saved-demo","target":"Map the application architecture","message":"Old release reminder","status":"expired","attempts":0,"queuedAt":"2026-09-09 11:00:00"}]}"#

    nonisolated static let detailJSON = #"""
    {
      "session":{"id":"demo","surface":"claude","name":"Make the build reliable","status":"busy","lastActive":"2026-09-07T12:05:00Z","cwd":"/Users/demo/projects/fieldnotes","source":"cli","transport":"local"},
      "exchanges":[],"capabilities":{"send":true,"stream":true,"reply":true,"goal":true,"compact":true,"model":true,"interrupt":true,"steer":true},
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
    override func startLoading() {
        var status = 200
        let body: String
        if let capture = SessionCapture.current {
            let id = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "id" })?.value ?? ""
            let captured: String?
            switch request.url?.path {
            case "/api/v1/snapshot": captured = capture.snapshotJSON
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
        case "/api/v1/session-options": body = #"{"surfaces":[{"id":"claude","workspace":true},{"id":"codex","workspace":true},{"id":"notion","workspace":false}],"workspaces":["/Users/demo/projects/fieldnotes"]}"#
        case "/api/v1/models": body = #"{"models":[{"id":"demo-model","displayName":"Example model"}]}"#
        case "/api/v1/snapshot": body = SessionPreview.snapshotJSON
        case "/api/v1/queue": body = SessionPreview.queueJSON
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
    override func stopLoading() {}
}
#endif
