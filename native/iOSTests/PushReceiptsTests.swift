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

    func testOnlyTheOldestWaitingReceiptsBeyondTheKeptOnesAreDropped() {
        let at = Date()
        let many = (0..<(PushReceipts.kept + 5)).map { PushReceipt(host: "h1", pushId: "p\($0)", receivedAt: at.addingTimeInterval(Double($0)), alerts: "on") }
        XCTAssertEqual(PushReceipts.overflow(many.shuffled()).map(\.pushId), (0..<5).map { "p\($0)" })
        XCTAssertTrue(PushReceipts.overflow(Array(many.prefix(PushReceipts.kept))).isEmpty)
        XCTAssertNotEqual(PushReceipts.account(many[0]), PushReceipts.account(PushReceipt(host: "h2", pushId: "p0", receivedAt: at, alerts: "on")), "the same id from another computer is another push")
    }

    func testForgettingAComputerDropsOnlyItsWaitingReceipts() {
        PushReceipts.pending().forEach(PushReceipts.remove)
        defer { PushReceipts.pending().forEach(PushReceipts.remove) }
        PushReceipts.enqueue(PushReceipt(host: "gone", pushId: "gone00001", receivedAt: Date(), alerts: "on"))
        PushReceipts.enqueue(PushReceipt(host: "kept", pushId: "kept00001", receivedAt: Date(), alerts: "on"))
        PushReceipts.forget(host: "gone")
        XCTAssertEqual(PushReceipts.pending().map(\.host), ["kept"])
    }

    func testAReceiptQueuedAfterItsComputerWasForgottenIsDroppedOnTheNextFlush() async {
        PushReceipts.pending().forEach(PushReceipts.remove)
        defer { PushReceipts.pending().forEach(PushReceipts.remove) }
        PushReceipts.enqueue(PushReceipt(host: "gone", pushId: "late00001", receivedAt: Date(), alerts: "on"))
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ReceiptStub.self]
        ReceiptStub.reset(status: 503)
        await PushReceipts.flush(credential: { _ in nil }, session: URLSession(configuration: configuration))
        XCTAssertTrue(PushReceipts.pending().isEmpty, "a receipt for a computer this phone no longer knows has nowhere to go")
        XCTAssertTrue(ReceiptStub.requests.isEmpty)
    }

    func testAReceiptQueuedWhileOthersAreSentIsKept() async {
        PushReceipts.pending().forEach(PushReceipts.remove)
        defer { PushReceipts.pending().forEach(PushReceipts.remove) }
        let at = Date()
        let first = PushReceipt(host: "h1", pushId: "first0001", receivedAt: at, alerts: "on")
        let second = PushReceipt(host: "h1", pushId: "second001", receivedAt: at, alerts: "on")
        PushReceipts.enqueue(first)
        PushReceipts.enqueue(second)
        XCTAssertEqual(Set(PushReceipts.pending().map(\.pushId)), ["first0001", "second001"], "two writers each keep their own receipt")

        let late = PushReceipt(host: "h1", pushId: "late00001", receivedAt: at, alerts: "on")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ReceiptStub.self]
        ReceiptStub.reset(status: 200)
        ReceiptStub.during = { PushReceipts.enqueue(late) }
        defer { ReceiptStub.during = nil }
        let paired = PairedHostCredential(hostID: "h1", hostName: "Mac", endpoint: URL(string: "https://mac.example.ts.net:7412")!, token: "device-token", pairedAt: at)
        await PushReceipts.flush(credential: { $0 == "h1" ? paired : nil }, session: URLSession(configuration: configuration))
        XCTAssertEqual(PushReceipts.pending().map(\.pushId), ["late00001"], "a flush removes only what it sent")
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
    nonisolated(unsafe) static var during: (() -> Void)?

    static func reset(status: Int) {
        requests = []
        self.status = status
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.requests.append(request)
        Self.during?()
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: Self.status, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(#"{"recorded":1}"#.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
