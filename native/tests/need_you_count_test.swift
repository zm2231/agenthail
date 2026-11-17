@testable import AgenthailHelpers
import AppKit
import Foundation

final class NeedYouDaemon: URLProtocol, @unchecked Sendable {
    static let decisions = #"{"decisions":[{"group":"waiting","sessionId":"lead","kind":"ask","reason":"Ship it?","answerable":true,"requests":[{"id":"ask-000000000001","kind":"ask","tool":"","title":"Ship it?","questions":[],"alwaysAllow":false}]},{"group":"waiting","sessionId":"worker","kind":"ask","reason":"Which table?","answerable":true,"requests":[{"id":"ask-000000000002","kind":"ask","tool":"","title":"Which table?","questions":[],"alwaysAllow":false}]}],"leadHandling":[]}"#

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body) = request.url?.path == "/api/v1/decisions" ? (200, Self.decisions) : (404, #"{"error":{"code":"unknown_route","message":"not in this test"}}"#)
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct NeedYouCountTest {
    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }

    static let snapshot = #"""
    {"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"codexRecentHours":24,
     "leadReplacements":[{"id":5,"projectId":"api","name":"lead","sessionId":"s-api-2","previousSessionId":"s-api","changedBy":"you","reason":"context full","at":"2026-11-08T10:10:00Z"}]}
    """#

    @MainActor
    static func main() async throws {
        _ = NSApplication.shared
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [NeedYouDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        model.snapshot = try JSONDecoder().decode(DashboardSnapshot.self, from: Data(snapshot.utf8))
        let family = ViewFamily(id: "lead", rootId: "lead", projectId: "api", checkoutId: "", members: [ViewMember(id: "lead", depth: 0, contextOnly: false, pinned: false), ViewMember(id: "worker", depth: 1, contextOnly: false, pinned: false)], pinned: false, workingSubagents: 0)
        model.adoptCurrentView(ScopeSessions(sessions: [], hostEpoch: "h", catalogSeq: 1, view: ScopePage.View(counts: [:], summary: ScopePage.Summary(queuedMessages: 0, attentionFamilies: 1, workingFamilies: 0), surfaces: [], projects: [], families: [family])))
        model.decisionFeed.refresh()
        for _ in 0..<200 where model.decisionFeed.count < 2 { try await Task.sleep(for: .milliseconds(10)) }

        let cards = model.inboxStops.entries.count
        check(cards == 3, "the Inbox lists the lead replacement and both asks in the one family: \(model.inboxStops.entries.map(\.paneID))")
        check(model.needsYouCount == cards, "the badge, pill and status count equal the Inbox cards, not the families that need the person: \(model.needsYouCount) for \(cards) cards")
        print("need-you count ok")
    }
}
