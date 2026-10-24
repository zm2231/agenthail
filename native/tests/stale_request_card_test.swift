@testable import AgenthailHelpers
import Combine
import Foundation

final class StaleRequestStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requestsReply: (Int, String) = (200, "{}")

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body): (Int, String)
        switch request.url?.path {
        case "/api/v1/requests": (status, body) = Self.requestsReply
        case "/api/v1/actions": (status, body) = (409, #"{"ok":false,"error":{"code":"stale","message":"this request was already answered or has expired"}}"#)
        default: (status, body) = (500, #"{"error":"not in this test"}"#)
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct StaleRequestCardTest {
    @MainActor
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StaleRequestStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let pane = model.openPane()
        var capabilities = Capabilities()
        capabilities.respond = true
        let session = SessionState(id: "S", surface: "claude", name: "s", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: capabilities, readOnly: nil, readOnlyReason: nil)
        pane.select("S")

        StaleRequestStub.requestsReply = (200, #"{"permissionMode":"","requests":[{"id":"dead","kind":"approval","tool":"Bash","alwaysAllow":false,"createdAt":"2026-10-20T02:27:07Z"},{"id":"live","kind":"approval","tool":"Read","alwaysAllow":false,"createdAt":"2026-10-20T02:28:00Z"}]}"#)
        await pane.syncRequests(for: session)
        guard let dead = pane.requests?.requests.first(where: { $0.id == "dead" }) else {
            fail("the pane did not load the requests: \(String(describing: pane.requests))")
        }

        StaleRequestStub.requestsReply = (500, #"{"error":"the daemon is restarting"}"#)
        await pane.respond(to: dead, decision: .allow)
        check(pane.requests?.requests.map(\.id) == ["live"], "a stale answer removes its card even when the reread fails: \(String(describing: pane.requests?.requests.map(\.id)))")

        let state = SessionRequests(permissionMode: "acceptEdits", requests: [dead])
        check(state.removing("dead").requests.isEmpty && state.removing("dead").permissionMode == "acceptEdits", "removing keeps the permission mode")
        check(state.removing("other") == state, "removing another id changes nothing")

        let quiet = SessionState(id: "S", surface: "claude", name: "s", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        await pane.syncRequests(for: quiet)
        check(pane.requests == nil, "a session that can't answer requests clears the cards")
        var published = 0
        let watch = pane.objectWillChange.sink { published += 1 }
        await pane.syncRequests(for: quiet)
        watch.cancel()
        check(published == 0, "resyncing a session that can't answer requests publishes nothing: \(published)")

        StaleRequestStub.requestsReply = (200, #"{"permissionMode":"","requests":[]}"#)
        var sets: [SessionRequests?] = []
        var errorSets = 0
        let requestWatch = pane.$requests.dropFirst().sink { sets.append($0) }
        let errorWatch = pane.$requestError.dropFirst().sink { _ in errorSets += 1 }
        await pane.syncRequests(for: session)
        check(sets.count == 1 && sets[0]?.requests.isEmpty == true, "reopening sets the loaded requests once: \(sets)")
        var waiting = session
        waiting.waitingOn = WaitingState(kind: "question", id: "q")
        await pane.syncRequests(for: waiting)
        requestWatch.cancel()
        errorWatch.cancel()
        check(sets.count == 1, "rereading the same requests sets nothing: \(sets.count)")
        check(errorSets == 0, "a request read with no error to clear sets nothing: \(errorSets)")
        print("stale request card tests passed")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fail(message) }
    }

    static func fail(_ message: String) -> Never {
        FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
        exit(1)
    }
}
