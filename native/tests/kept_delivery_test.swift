@testable import AgenthailHelpers
import Foundation

@main
struct KeptDeliveryTest {
    static func check(_ ok: Bool, _ what: String) {
        if !ok { print("FAIL: \(what)"); exit(1) }
    }

    static func main() {
        let refused = AgenthailAPIError.request(502, "the desktop app closed")
        let kept = KeptDelivery.wrapping(refused, body: Data(#"{"error":{"code":"bad_gateway","message":"the desktop app closed"},"askId":"ask-0fca204fdeb3"}"#.utf8)) as? KeptDelivery
        check(kept?.askID == "ask-0fca204fdeb3", "a kept failed delivery names its ask")
        check(kept.map(AgenthailAPIError.userMessage) == "the desktop app closed", "the wrapped error keeps the daemon's message")

        let nothingKept = KeptDelivery.wrapping(refused, body: Data(#"{"error":{"code":"invalid_request","message":"no running turn"}}"#.utf8))
        check(!(nothingKept is KeptDelivery), "no askId means nothing is kept on the daemon")
        check(!(KeptDelivery.wrapping(refused, body: Data(#"{"error":{"code":"bad_gateway","message":"x"},"askId":""}"#.utf8)) is KeptDelivery), "an empty askId keeps nothing")
        check(!(KeptDelivery.wrapping(refused, body: Data("not json".utf8)) is KeptDelivery), "an unreadable body is left alone")

        let question = SessionRequest.Question(question: #"Message not delivered: "Run the tests""#, header: "", multiSelect: false, options: [
            SessionRequest.Option(id: "delete", label: "Delete", description: ""), SessionRequest.Option(id: "dismiss", label: "Dismiss", description: ""),
        ])
        let offered = SessionRequests(permissionMode: "", requests: [SessionRequest(id: "ask-0fca204fdeb3", kind: "ask", tool: "", questions: [question])])
        let dismissOnly = SessionRequests(permissionMode: "", requests: [SessionRequest(id: "ask-0fca204fdeb3", kind: "ask", tool: "", questions: [SessionRequest.Question(question: "q", header: "", multiSelect: false, options: [SessionRequest.Option(id: "dismiss", label: "Dismiss", description: "")])])])

        var send = LocalSend(text: "Run the tests", sentAt: Date(), status: nil)
        check(!send.canDelete, "a send that did not fail has no Delete")
        send.failure = "the desktop app closed"
        check(send.canDelete, "a failure that left nothing on the daemon is removed locally")
        send.ask = FailedSendAsk(id: "ask-0fca204fdeb3")
        check(!send.canDelete, "Delete waits until the ask is read")
        send.ask = send.ask?.reading(offered)
        check(send.canDelete && send.ask?.choices == ["delete", "dismiss"], "an ask that offers delete shows Delete")
        send.ask = send.ask?.reading(dismissOnly)
        check(!send.canDelete, "an ask without delete shows no Delete: it may have reached the session")
        send.ask = send.ask?.reading(SessionRequests(permissionMode: "", requests: []))
        check(!send.canDelete, "an ask that is gone shows no Delete")
        print("ok")
    }
}
