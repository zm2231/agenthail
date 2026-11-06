@testable import AgenthailHelpers
import Foundation

final class AskDaemon: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var answered = false
    nonisolated(unsafe) static var reply: (Int, String) = (200, #"{"ok":true,"result":{"requestId":"turn-end-1","outcome":"answered","receipt":{}}}"#)
    nonisolated(unsafe) static var posts: [(body: [String: Any], key: String?)] = []

    static func ask(_ id: String) -> String {
        #"{"permissionMode":"","requests":[{"id":"\#(id)","kind":"ask","tool":"","title":"Ready to ship","prompt":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"label":"Deploy","description":""},{"label":"Wait","description":"hold until Monday"}],"suggested":0,"links":[{"title":"Runbook","url":"https://example.com/run"}]}],"actions":[{"id":"o0","title":"Deploy"},{"id":"o1","title":"Wait"},{"id":"reply","title":"Reply…"}],"alwaysAllow":false,"createdAt":"2026-11-06T04:52:30Z"}]}"#
    }
    nonisolated(unsafe) static var listed = "turn-end-1"
    nonisolated(unsafe) static var staleReread = false

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body): (Int, String)
        switch request.url?.path {
        case "/api/v1/requests":
            (status, body) = (200, Self.answered && !Self.staleReread ? #"{"permissionMode":"","requests":[]}"# : Self.ask(Self.listed))
        case "/api/v1/actions":
            let data = request.httpBody ?? request.httpBodyStream.map(Self.read) ?? Data()
            Self.posts.append(((try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:], request.value(forHTTPHeaderField: "Idempotency-Key")))
            (status, body) = Self.reply
            if status == 200 || body.contains("may have reached") { Self.answered = true }
        default:
            (status, body) = (500, #"{"error":"not in this test"}"#)
        }
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    static func read(_ stream: InputStream) -> Data {
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            data.append(buffer, count: count)
        }
        return data
    }
}

@main
struct AskCardFlowTest {
    @MainActor
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [AskDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let pane = model.openPane()
        let session = row(requestID: "turn-end-1")
        pane.select("S")
        await pane.syncRequests(for: session)

        guard let request = pane.requests?.offered(pendingAsk: session.pendingAskID).first else {
            fail("a session answering nothing through its provider still shows its ask: \(String(describing: pane.requests))")
        }
        check(request.isAsk && RequestCard.heading(request, asker: session.displayName) == "@builder asks: Ready to ship", "the card is the ask, headed by the session")

        var pager = QuestionPagerDrafts.shared.pager(for: request)
        check(pager.ask && !pager.isOneTap, "the card's pager treats it as an ask, with no Decline")
        check(pager.pick(1) == nil, "picking an option sends nothing")
        check(AskDaemon.posts.isEmpty, "nothing reached the daemon on a pick")
        pager.note = "After lunch."
        guard let decision = pager.submit() else { fail("Submit produced no answer") }
        await pane.respond(to: request, decision: decision)

        check(AskDaemon.posts.count == 1, "Submit sends one answer: \(AskDaemon.posts.count)")
        let post = AskDaemon.posts[0]
        check(post.body["action"] as? String == "respond" && post.body["sessionId"] as? String == "S" && post.body["requestId"] as? String == "turn-end-1", "the answer goes through respond: \(post.body)")
        let sent = post.body["decision"] as? [String: Any] ?? [:]
        check(sent["behavior"] as? String == "allow" && (sent["answers"] as? [String: String]) == ["Deploy it now?": "Wait"] && sent["message"] as? String == "After lunch." && sent.count == 3, "the payload is the contract's: \(sent)")
        check(!(post.key ?? "").isEmpty, "the answer carries an Idempotency-Key")
        check(pane.requestError == nil, "a sent answer shows no error: \(String(describing: pane.requestError))")
        check(pane.requests?.offered(pendingAsk: session.pendingAskID).isEmpty == true, "the answered ask is gone after the reread")

        AskDaemon.answered = false
        AskDaemon.listed = "turn-end-1"
        await pane.syncRequests(for: row(requestID: "turn-end-1", answered: true))
        check(pane.requests == nil, "an answered row neither fetches nor offers its ask, even if the daemon still listed it")

        AskDaemon.listed = "turn-end-2"
        AskDaemon.reply = (502, #"{"ok":false,"error":{"code":"bad_gateway","message":"the answer may have reached the session, so it is not sent again: timeout"}}"#)
        let newer = row(requestID: "turn-end-2")
        await pane.syncRequests(for: newer)
        guard let second = pane.requests?.offered(pendingAsk: newer.pendingAskID).first else { fail("a new ask was not read again") }
        var again = QuestionPagerDrafts.shared.pager(for: second)
        _ = again.pick(0)
        await pane.respond(to: second, decision: again.submit()!)
        check(AskDaemon.posts.count == 2, "the answer was sent once: \(AskDaemon.posts.count)")
        check(pane.requestError == RequestAnswerFailure.maybeDeliveredMessage, "a 502 that may have arrived says so: \(String(describing: pane.requestError))")
        check(pane.requests?.offered(pendingAsk: newer.pendingAskID).isEmpty == true, "and is not offered again")

        AskDaemon.answered = false
        AskDaemon.listed = "turn-end-3"
        AskDaemon.reply = (502, #"{"ok":false,"error":{"code":"bad_gateway","message":"session is not running"}}"#)
        let third = row(requestID: "turn-end-3")
        await pane.syncRequests(for: third)
        guard let open = pane.requests?.offered(pendingAsk: third.pendingAskID).first else { fail("the third ask was not read") }
        var retry = QuestionPagerDrafts.shared.pager(for: open)
        _ = retry.pick(1)
        await pane.respond(to: open, decision: retry.submit()!)
        check(pane.requestError == "session is not running" && pane.requests?.offered(pendingAsk: third.pendingAskID).map(\.id) == ["turn-end-3"], "a 502 that surely failed keeps the card to answer again")

        AskDaemon.reply = (409, #"{"ok":false,"error":{"code":"stale","message":"already answered"}}"#)
        await pane.respond(to: open, decision: retry.submit()!)
        check(pane.requestError == RequestAnswerFailure.staleMessage, "409 stale says it was already answered: \(String(describing: pane.requestError))")
        check(pane.requests?.offered(pendingAsk: third.pendingAskID, retired: pane.retiredAsks.retired(in: "S")).isEmpty == true, "a 409 drops the card")

        AskDaemon.staleReread = true
        for (id, reply, message) in [("turn-end-4", (409, #"{"ok":false,"error":{"code":"stale","message":"already answered"}}"#), RequestAnswerFailure.staleMessage),
                                     ("turn-end-5", (502, #"{"ok":false,"error":{"code":"bad_gateway","message":"the answer may have reached the session, so it is not sent again: timeout"}}"#), RequestAnswerFailure.maybeDeliveredMessage),
                                     ("turn-end-6", (200, #"{"ok":true,"result":{"requestId":"turn-end-6","outcome":"answered","receipt":{}}}"#), nil)] {
            AskDaemon.answered = false
            AskDaemon.listed = id
            AskDaemon.reply = reply
            let stale = row(requestID: id)
            await pane.syncRequests(for: stale)
            guard let ask = pane.requests?.offered(pendingAsk: stale.pendingAskID, retired: pane.retiredAsks.retired(in: "S")).first else { fail("\(id) was not read") }
            var card = QuestionPagerDrafts.shared.pager(for: ask)
            _ = card.pick(0)
            await pane.respond(to: ask, decision: card.submit()!)
            check(pane.requests?.requests.contains { $0.id == id } == true, "the reread still lists \(id), as a stale daemon read can")
            check(pane.requests?.offered(pendingAsk: stale.pendingAskID, retired: pane.retiredAsks.retired(in: "S")).isEmpty == true, "\(id) is not offered again while the row still names it")
            check(pane.requestError == message, "\(id) keeps its message: \(String(describing: pane.requestError))")
        }
        await pane.syncRequests(for: row(requestID: "turn-end-6", answered: true))
        check(pane.retiredAsks.retired(in: "S") == nil, "the local entry goes once the row shows the answer")
        AskDaemon.staleReread = false
        AskDaemon.answered = false
        AskDaemon.listed = "turn-end-7"
        let next = row(requestID: "turn-end-7")
        await pane.syncRequests(for: next)
        check(pane.requests?.offered(pendingAsk: next.pendingAskID, retired: pane.retiredAsks.retired(in: "S")).map(\.id) == ["turn-end-7"], "the next ask is offered")
        print("ask card flow ok")
    }

    static func row(requestID: String, answered: Bool = false) -> SessionState {
        let answeredAt = answered ? #","answeredAt":"2026-11-06T04:55:02Z""# : ""
        let json = #"{"id":"S","surface":"codex","name":"builder","alias":"builder","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false,"respond":false},"endTurn":{"headline":"Ready to ship","needs":"you","endedAt":"2026-11-06T04:52:30Z","requestId":""# + requestID + "\"" + answeredAt + "}}"
        return try! JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fail(message) }
    }

    static func fail(_ message: String) -> Never {
        FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
        exit(1)
    }
}
