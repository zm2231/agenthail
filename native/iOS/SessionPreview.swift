#if DEBUG
import SwiftUI

// Public demo data exercises the real session views without pairing or network access.
struct SessionPreview: View {
    @StateObject private var model: AgenthailIOSModel
    private let session: SessionState
    private let detail: SessionDetail

    init() {
        let decoder = JSONDecoder()
        let detail = try! decoder.decode(SessionDetail.self, from: Data(Self.detailJSON.utf8))
        self.detail = detail
        session = SessionState(id: "demo", surface: "claude", name: "Make the build reliable", alias: nil, status: "busy", lastActive: nil,
                               queueCount: 1, open: true, current: true, currentReason: nil, capabilities: detail.capabilities, readOnly: false, readOnlyReason: nil)
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [SessionPreviewProtocol.self]
        let model = AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://preview.invalid")!, token: "public-demo", session: URLSession(configuration: config)))
        model.selectedSessionID = "demo"
        model.selectedDetail = detail
        model.composer = "Keep the regression check with the fix."
        model.deliveryStatus["demo"] = "One follow-up queued"
        let sessionJSON = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(session))
        let snapshot: [String: Any] = ["updatedAt": "2026-09-07T12:05:00Z", "daemon": ["running": true], "surfaces": [], "sessions": [sessionJSON], "totalSessions": 1, "queue": [], "channels": [], "relays": [], "history": [], "attention": [], "codexRecentHours": 24]
        model.snapshot = try! decoder.decode(DashboardSnapshot.self, from: JSONSerialization.data(withJSONObject: snapshot))
        _model = StateObject(wrappedValue: model)
    }
    var body: some View {
        if ProcessInfo.processInfo.arguments.contains("--preview-new") {
            NewSessionSheet(model: model)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-inspector") {
            SessionInspector(model: model, session: session, detail: detail)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-workspace") {
            ConversationFlow(model: model, initialSessionID: "demo").tint(.orange)
        } else {
            NavigationStack { SessionScreen(model: model, session: session) }.tint(.orange)
        }
    }
    static let detailJSON = #"""
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
        switch request.url?.path {
        case "/api/v1/session-options": body = #"{"surfaces":[{"id":"codex","workspace":true},{"id":"notion","workspace":false}],"workspaces":["/Users/demo/projects/fieldnotes"]}"#
        case "/api/v1/models": body = #"{"models":[{"id":"demo-model","displayName":"Example model"}]}"#
        case "/api/v1/session": body = SessionPreview.detailJSON
        default: status = 503; body = #"{"error":{"message":"This public preview does not execute actions."}}"#
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
#endif
