@testable import AgenthailHelpers
import Foundation

final class FailedSendDeleteStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var actionReply: (Int, String) = (200, "{}")

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let (status, body): (Int, String)
        switch request.url?.path {
        case "/api/v1/actions": (status, body) = Self.actionReply
        case "/api/v1/requests": (status, body) = (200, #"{"permissionMode":"","requests":[]}"#)
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
struct FailedSendDeleteTest {
    static func check(_ ok: Bool, _ what: String) {
        if !ok { print("FAIL: \(what)"); exit(1) }
    }

    static func failedSend() -> LocalSend {
        var send = LocalSend(text: "/Volumes/4/CF holds 680 GiB", sentAt: Date(), status: nil)
        send.failure = "steer requires a message, not a slash command"
        send.ask = FailedSendAsk(id: "ask-1", choices: [FailedSendAsk.delete, "dismiss"])
        return send
    }

    @MainActor
    static func main() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [FailedSendDeleteStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        let pane = model.openPane()
        pane.select("S")

        FailedSendDeleteStub.actionReply = (200, #"{"ok":true,"result":{"requestId":"ask-1","outcome":"answered","choice":"delete"}}"#)
        model.outbox.record(failedSend(), in: "S")
        let question = SessionRequest.Question(question: #"Message not delivered: "/Volumes/4/CF holds 680 GiB""#, header: "", multiSelect: false, options: [
            SessionRequest.Option(id: "delete", label: "Delete", description: ""), SessionRequest.Option(id: "dismiss", label: "Dismiss", description: ""),
        ])
        await pane.respond(to: SessionRequest(id: "ask-1", kind: "ask", tool: "", questions: [question]), decision: .choose(FailedSendAsk.delete))
        check(model.outbox.rows["S"]?.isEmpty ?? true, "Delete on the pinned ask removes the failed send's row")

        func deleteAfterSettled(_ reply: String) async -> LocalSend {
            FailedSendDeleteStub.actionReply = (409, reply)
            model.operationError = nil
            let row = failedSend()
            model.outbox.record(row, in: "S")
            model.deleteSend(row, in: "S")
            for _ in 0..<200 where model.outbox.rows["S"]?.contains(where: { $0.id == row.id }) == true && model.operationError == nil {
                try? await Task.sleep(for: .milliseconds(10))
            }
            return row
        }
        let stale = #"{"ok":false,"error":{"code":"stale","message":"this request was already answered or has expired"}"#

        _ = await deleteAfterSettled(stale + #","settled":"delete"}"#)
        check(model.outbox.rows["S"]?.isEmpty ?? true, "Delete on a row whose ask was deleted elsewhere removes the row")
        check(model.operationError == nil, "an ask deleted elsewhere is not reported as a failure")

        _ = await deleteAfterSettled(stale + #","settled":"retry"}"#)
        check(model.outbox.rows["S"]?.isEmpty ?? true, "a row whose message is being sent again is no longer shown as not sent")
        check(model.operationError == SettledAsk.resentNotice, "Delete says the message was already sent again rather than claiming it was deleted")

        let kept = await deleteAfterSettled(stale + "}")
        check(model.outbox.rows["S"]?.contains { $0.id == kept.id } == true, "a stale reply that does not say how the ask was settled keeps the row")
        model.outbox.discard(kept, in: "S")

        model.outbox.record(failedSend(), in: "S")
        NotificationCenter.default.post(name: .agenthailRequestAnswered, object: AnsweredRequest(requestID: "ask-1", choice: FailedSendAsk.delete))
        for _ in 0..<200 where !(model.outbox.rows["S"]?.isEmpty ?? true) {
            try? await Task.sleep(for: .milliseconds(10))
        }
        check(model.outbox.rows["S"]?.isEmpty ?? true, "Delete from a notification removes the failed send's row")
        print("ok")
    }
}
