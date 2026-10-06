import Foundation

@main
struct SessionLaunchRetryTest {
    @MainActor
    static func main() async {
        let a = SessionLaunchForm(agent: "claude", folder: "/work/a", message: "Synthetic task A")
        var b = a
        b.folder = "/work/b"

        var model = makeModel([.lost, .lost])
        _ = await model.launchSession(a)
        _ = await model.launchSession(a)
        check(ScriptedProtocol.keys[0] == ScriptedProtocol.keys[1], "a retry after a lost connection reuses the key")

        model = makeModel([.unknown, .unknown])
        let unknown = await model.launchSession(a)
        _ = await model.launchSession(a)
        check(unknown == .uncertain("Agenthail couldn't confirm the session started. Check the sidebar before trying again."), "an unknown receipt is uncertain")
        check(ScriptedProtocol.keys[0] == ScriptedProtocol.keys[1], "a retry after an unknown receipt reuses the key")

        model = makeModel([.lost, .lost, .lost])
        _ = await model.launchSession(a)
        _ = await model.launchSession(b)
        _ = await model.launchSession(a)
        check(Set(ScriptedProtocol.keys).count == 3, "changing the request mints a new key, and changing it back does too")

        var spelled = a
        spelled.alias = ""
        spelled.message = "  Synthetic task A\n"
        model = makeModel([.lost, .lost])
        _ = await model.launchSession(a)
        _ = await model.launchSession(spelled)
        check(ScriptedProtocol.keys[0] == ScriptedProtocol.keys[1], "edits that send the same request keep the key")

        model = makeModel([.refused, .lost])
        let refused = await model.launchSession(a)
        _ = await model.launchSession(a)
        check(refused == .failed("no such folder"), "a daemon refusal is a definite failure")
        check(ScriptedProtocol.keys[0] != ScriptedProtocol.keys[1], "a retry after a definite error uses a new key")

        model = makeModel([.failedReceipt, .lost])
        let failed = await model.launchSession(a)
        _ = await model.launchSession(a)
        check(failed == .failed("synthetic start failure"), "a failure receipt is a definite failure")
        check(ScriptedProtocol.keys[0] != ScriptedProtocol.keys[1], "a retry after a failure receipt uses a new key")

        model = makeModel([.replayedSubmission, .lost])
        let replayed = await model.launchSession(a)
        _ = await model.launchSession(a)
        check(replayed == .starting("Agenthail is still starting this session. It appears in the sidebar once it starts."), "a replayed in-flight create shows it is still starting")
        check(ScriptedProtocol.keys[0] == ScriptedProtocol.keys[1], "a retry while the first create is still starting reuses the key")

        model = makeModel([.accepted, .lost])
        _ = await model.launchSession(a)
        _ = await model.launchSession(a)
        check(ScriptedProtocol.keys[0] != ScriptedProtocol.keys[1], "an accepted launch releases the key")
        print("PASS")
    }

    @MainActor
    private static func makeModel(_ script: [ScriptedProtocol.Reply]) -> AgenthailModel {
        ScriptedProtocol.script = script
        ScriptedProtocol.keys = []
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ScriptedProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "synthetic", session: URLSession(configuration: configuration))
        return AgenthailModel(connecting: false, api: api)
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}

final class ScriptedProtocol: URLProtocol {
    enum Reply {
        case lost
        case unknown
        case refused
        case failedReceipt
        case replayedSubmission
        case accepted
    }

    nonisolated(unsafe) static var script: [Reply] = []
    nonisolated(unsafe) static var keys: [String] = []

    override class func canInit(with request: URLRequest) -> Bool { request.url?.path == "/api/v1/actions" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.keys.append(request.value(forHTTPHeaderField: "Idempotency-Key") ?? "")
        switch Self.script.removeFirst() {
        case .lost:
            client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
        case .unknown:
            reply(200, #"{"ok":false,"unknown":true}"#)
        case .refused:
            reply(400, "no such folder")
        case .failedReceipt:
            reply(502, #"{"ok":false,"status":"failed","error":"synthetic start failure"}"#)
        case .replayedSubmission:
            reply(202, #"{"ok":true,"status":"submitted"}"#)
        case .accepted:
            reply(202, #"{"ok":true,"status":"submitted","accepted":true,"retryable":false,"launcher":"tmux"}"#)
        }
    }

    override func stopLoading() {}

    private func reply(_ status: Int, _ body: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
