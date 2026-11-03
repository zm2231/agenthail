import XCTest
@testable import Agenthail

private final class InboxHostProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requests: [String] = []
    private static let lock = NSLock()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        Self.lock.withLock { Self.requests.append(url.host! + "?" + (url.query ?? "")) }
        if url.host == "slow.invalid" { return }
        let query = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let before = query.first { $0.name == "before" }?.value
        let body: String
        if url.host == "older.invalid" {
            body = before == nil
                ? #"{"items":[\#(Self.item(9, session: "s1", status: "pending", at: "2026-11-02 10:00:00")),\#(Self.item(8, session: "s1", status: "delivered", at: "2026-11-01 10:00:00")),\#(Self.item(7, session: "s2", status: "delivered", at: "2026-11-01 09:00:00"))],"nextBefore":8}"#
                : #"{"items":[\#(Self.item(3, session: "s1", status: "delivered", at: "2026-08-01 10:00:00"))]}"#
        } else {
            body = #"{"items":[\#(Self.item(5, session: "s1", status: "delivered", at: "2026-11-02 08:00:00"))]}"#
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    static func item(_ id: Int, session: String, status: String, at: String) -> String {
        #"{"id":\#(id),"sessionId":"\#(session)","target":"@\#(session)","message":"m\#(id)","status":"\#(status)","attempts":1,"queuedAt":"\#(at)","historical":\#(status == "delivered"),"evidence":"\#(status == "delivered" ? "delivered" : "queued")"}"#
    }
}

@MainActor
final class InboxPageTests: XCTestCase {
    private func host(_ name: String) -> InboxPage.Host {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [InboxHostProtocol.self]
        return InboxPage.Host(id: name, name: name, api: AgenthailAPI(baseURL: URL(string: "https://\(name).invalid")!, token: "t", session: URLSession(configuration: configuration)))
    }

    func testAnUnreachableHostDoesNotHoldTheOthersPastItsShortTimeout() async throws {
        InboxHostProtocol.requests = []
        let started = Date()
        var partials: [(TimeInterval, Int)] = []
        let page = try await InboxPage.load(from: [host("slow"), host("older"), host("fast")], sessionID: nil) { partial in
            partials.append((Date().timeIntervalSince(started), partial.items.count))
        }
        let elapsed = Date().timeIntervalSince(started)
        XCTAssertLessThan(elapsed, InboxPage.hostTimeout + 3)
        XCTAssertEqual(partials.count, 2)
        XCTAssertEqual(partials.last?.1, 4)
        XCTAssertLessThan(try XCTUnwrap(partials.last).0, 2, "the reachable hosts show before the unreachable one times out")
        XCTAssertEqual(page.unreachable, ["slow"])
        XCTAssertEqual(page.items.map(\.listID), ["older/9", "fast/5", "older/8", "older/7"])
        XCTAssertEqual(page.cursors, ["older": 8])
    }

    func testASessionInboxAsksForThatSessionAndPagesOlderHistory() async throws {
        InboxHostProtocol.requests = []
        let first = try await InboxPage.load(from: [host("older"), host("fast")], sessionID: "older::s1")
        XCTAssertTrue(InboxHostProtocol.requests.allSatisfy { $0.contains("sessionId=older::s1") })
        XCTAssertEqual(first.items.map(\.listID), ["older/9", "older/8"])
        XCTAssertTrue(first.hasOlder)

        InboxHostProtocol.requests = []
        let older = try await InboxPage.load(from: [host("older"), host("fast")], sessionID: "older::s1", before: first.cursors)
        XCTAssertEqual(InboxHostProtocol.requests, ["older.invalid?sessionId=older::s1&before=8"])
        let merged = first.appending(older)
        XCTAssertEqual(merged.items.map(\.listID), ["older/9", "older/8", "older/3"])
        XCTAssertFalse(merged.hasOlder)

        let refreshed = merged.refreshed(with: first)
        XCTAssertEqual(refreshed.items.map(\.listID), ["older/9", "older/8", "older/3"])
        XCTAssertFalse(refreshed.hasOlder)
    }
}
