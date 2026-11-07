@testable import AgenthailHelpers
import Foundation

@main
struct KeptDeliveryTest {
    static func check(_ ok: Bool, _ what: String) {
        if !ok { print("FAIL: \(what)"); exit(1) }
    }

    static func main() {
        let refused = AgenthailAPIError.request(502, "the desktop app closed")
        let kept = KeptDelivery.wrapping(refused, body: Data(#"{"error":{"code":"bad_gateway","message":"the desktop app closed"},"deliveryId":1,"deletable":true}"#.utf8)) as? KeptDelivery
        check(kept?.deliveryID == 1 && kept?.deletable == true, "a kept failed delivery carries its id and deletable flag")
        check(kept.map(AgenthailAPIError.userMessage) == "the desktop app closed", "the wrapped error keeps the daemon's message")

        let mayHaveArrived = KeptDelivery.wrapping(refused, body: Data(#"{"error":{"code":"bad_gateway","message":"x"},"deliveryId":4,"deletable":false}"#.utf8)) as? KeptDelivery
        check(mayHaveArrived?.deletable == false, "deletable false is kept")

        let nothingKept = KeptDelivery.wrapping(refused, body: Data(#"{"error":{"code":"invalid_request","message":"no running turn"}}"#.utf8))
        check(!(nothingKept is KeptDelivery), "no deliveryId means nothing is kept on the daemon")
        check(!(KeptDelivery.wrapping(refused, body: Data("not json".utf8)) is KeptDelivery), "an unreadable body is left alone")

        var send = LocalSend(text: "Run the tests", sentAt: Date(), status: nil)
        check(!send.canDelete, "a send that did not fail has no Delete")
        send.failure = "the desktop app closed"
        check(send.canDelete, "an app-side failure with no delivery id can be removed locally")
        send.deliveryID = 4
        check(!send.canDelete, "a delivery that may have reached the session has no Delete")
        send.deletable = true
        check(send.canDelete, "a deletable delivery has Delete")
        print("ok")
    }
}
