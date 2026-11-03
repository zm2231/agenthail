import Foundation
import UserNotifications
import XCTest
@testable import Agenthail

final class PushReceiptsTests: XCTestCase {
    func testAReceiptQuotesThePushAndWhenItArrived() throws {
        let arrived = Date(timeIntervalSince1970: 1_793_660_000.25)
        let body = PushReceipts.body([PushReceipt(host: "h1", pushId: "abcdefgh2345", receivedAt: arrived, alerts: "on")])
        let decoded = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: [[String: String]]])
        let receipt = try XCTUnwrap(decoded["receipts"]?.first)
        XCTAssertEqual(receipt["pushId"], "abcdefgh2345")
        XCTAssertEqual(receipt["alerts"], "on")
        XCTAssertEqual(receipt["host"], nil, "the computer knows which host it is")
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        XCTAssertEqual(try XCTUnwrap(formatter.date(from: try XCTUnwrap(receipt["receivedAt"]))).timeIntervalSince1970, arrived.timeIntervalSince1970, accuracy: 0.001)
    }

    func testWaitingReceiptsKeepOneOfEachAndOnlyTheNewest() {
        let at = Date()
        let one = PushReceipt(host: "h1", pushId: "p1", receivedAt: at, alerts: "on")
        XCTAssertEqual(PushReceipts.merged([one], adding: [one]), [one], "a receipt queued twice is sent once")
        let other = PushReceipt(host: "h2", pushId: "p1", receivedAt: at, alerts: "on")
        XCTAssertEqual(PushReceipts.merged([one], adding: [other]).count, 2, "the same id from another computer is another push")
        let many = (0..<(PushReceipts.kept + 5)).map { PushReceipt(host: "h1", pushId: "p\($0)", receivedAt: at, alerts: "on") }
        let kept = PushReceipts.merged([], adding: many)
        XCTAssertEqual(kept.count, PushReceipts.kept)
        XCTAssertEqual(kept.last?.pushId, "p\(PushReceipts.kept + 4)")
    }

    func testAlertsAreOnOnlyWhenAgenthailMayShowThem() {
        XCTAssertEqual(PushReceipts.alerts(authorization: .authorized, alert: .enabled), "on")
        XCTAssertEqual(PushReceipts.alerts(authorization: .provisional, alert: .enabled), "on")
        XCTAssertEqual(PushReceipts.alerts(authorization: .authorized, alert: .disabled), "off")
        XCTAssertEqual(PushReceipts.alerts(authorization: .denied, alert: .enabled), "off")
    }

    func testAReceiptGoesToTheComputerThatSentThePush() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ReceiptStub.self]
        let session = URLSession(configuration: configuration)
        let receipt = PushReceipt(host: "h1", pushId: "abcdefgh2345", receivedAt: Date(), alerts: "on")
        ReceiptStub.reset(status: 200)
        let sent = await PushReceipts.post([receipt], endpoint: URL(string: "https://mac.example.ts.net:7412/api/v1/snapshot?x=1")!, token: "device-token", session: session)
        XCTAssertTrue(sent)
        let request = ReceiptStub.requests.first
        XCTAssertEqual(request?.httpMethod, "POST")
        XCTAssertEqual(request?.url?.absoluteString, "https://mac.example.ts.net:7412/api/v1/device/push-receipts")
        XCTAssertEqual(request?.value(forHTTPHeaderField: "Authorization"), "Bearer device-token")

        ReceiptStub.reset(status: 503)
        let refused = await PushReceipts.post([receipt], endpoint: URL(string: "https://mac.example.ts.net:7412")!, token: "device-token", session: session)
        XCTAssertFalse(refused, "a receipt the computer did not take waits to be sent again")
    }
}

private final class ReceiptStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requests: [URLRequest] = []
    nonisolated(unsafe) static var status = 200

    static func reset(status: Int) {
        requests = []
        self.status = status
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.requests.append(request)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: Self.status, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(#"{"recorded":1}"#.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
