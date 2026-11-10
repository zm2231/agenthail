import XCTest
@testable import Agenthail

private final class DecisionHostProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var requests: [String] = []
    private static let lock = NSLock()

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() { served(reply) }

    private func reply() {
        let url = request.url!
        let host = url.host!
        Self.lock.withLock { Self.requests.append(host + url.path + "?" + (url.query ?? "")) }
        if host == "down.invalid" {
            client?.urlProtocol(self, didFailWithError: URLError(.timedOut))
            return
        }
        let remote = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "host" }?.value
        let (status, body) = Self.answer(host: host, remote: remote)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    static func answer(host: String, remote: String?) -> (Int, String) {
        switch (host, remote) {
        case ("old.invalid", _):
            return (404, #"{"error":{"code":"unknown_route","message":"no route"}}"#)
        case ("laptop.invalid", "server"):
            return (200, #"{"decisions":[{"group":"stopped","sessionId":"far","kind":"usageLimit","reason":"Usage limit until 3:00","since":"2026-03-01T09:01:00Z","answerable":false}],"leadHandling":[]}"#)
        case ("laptop.invalid", _):
            return (200, """
            {"decisions":[
              {"group":"waiting","sessionId":"late","projectId":"web-app","kind":"request","reason":"Needs your approval","since":"2026-03-01T09:09:00Z","answerable":true,
               "requests":[{"id":"r-late","kind":"approval","tool":"Bash","alwaysAllow":true,"prompt":"Bash: make","actions":[{"id":"allow","title":"Allow"}]}]},
              {"group":"holding","sessionId":"lead","projectId":"web-app","kind":"ask","reason":"Asks: which layout","since":"2026-03-01T09:05:00Z","answerable":true,"holdsUp":["worker"],
               "requests":[{"id":"r-ask","kind":"ask","tool":"","title":"Which layout?","alwaysAllow":false,"questions":[{"question":"Which layout?","header":"Layout","multiSelect":false,"options":[{"id":"o0","label":"A","description":""},{"id":"o1","label":"B","description":""}],"suggested":0}]}]}
            ],
            "leadHandling":[{"sessionId":"worker","projectId":"web-app","heldBy":"lead","role":"lead","reason":"Needs your approval","since":"2026-03-01T09:06:00Z"}]}
            """)
        default:
            return (200, #"{"decisions":[],"leadHandling":[]}"#)
        }
    }
}

final class DecisionInboxTests: XCTestCase {
    override func setUp() {
        DecisionHostProtocol.requests = []
    }

    private func host(_ name: String, remote: String? = nil, id: String? = nil) -> InboxPage.Host {
        InboxPage.Host(id: id ?? name, name: name, api: AgenthailAPI(baseURL: URL(string: "https://\(name).invalid")!, token: "t", session: FixtureScope.open(self).session([DecisionHostProtocol.self])), remote: remote)
    }

    func testDecisionsFromEveryComputerAreMergedQualifiedAndOrderedByGroupThenAge() async {
        let inbox = await DecisionInbox.load(from: [host("laptop"), host("laptop", remote: "server", id: "hserver")])
        XCTAssertEqual(inbox.decisions.map(\.sessionId), ["hserver::far", "laptop::lead", "laptop::late"])
        XCTAssertEqual(inbox.decisions.map(\.group), [.stopped, .holding, .waiting])
        XCTAssertEqual(inbox.decisions[1].holdsUp, ["laptop::worker"])
        XCTAssertEqual(inbox.decisions[1].projectId, "laptop::web-app")
        XCTAssertEqual(inbox.decisions[1].hostID, "laptop")
        XCTAssertEqual(inbox.decisions[1].openRequests.first?.questions?.first?.suggested, 0)
        XCTAssertEqual(inbox.decisions[2].openRequests.map(\.id), ["r-late"])
        XCTAssertFalse(inbox.decisions[0].answerable)
        XCTAssertTrue(DecisionHostProtocol.requests.contains("laptop.invalid/api/v1/decisions?host=server"), "\(DecisionHostProtocol.requests)")
    }

    func testAPromptALeadIsHandlingIsListedApartWithWhoHoldsIt() async {
        let inbox = await DecisionInbox.load(from: [host("laptop")])
        XCTAssertEqual(inbox.leadHandling.map(\.sessionId), ["laptop::worker"])
        XCTAssertEqual(inbox.leadHandling.first?.heldBy, "laptop::lead")
        XCTAssertFalse(inbox.decisions.contains { $0.sessionId == "laptop::worker" })
    }

    func testAnUnreachableComputerAndAnOlderOneAreNamedWithoutHoldingTheRest() async {
        let inbox = await DecisionInbox.load(from: [host("down"), host("old"), host("laptop")])
        XCTAssertEqual(inbox.unreachable, ["down"])
        XCTAssertEqual(inbox.outdated, ["old"])
        XCTAssertEqual(inbox.decisions.count, 2)
    }

    func testRemovingADecisionDropsOnlyThatSession() async {
        let inbox = await DecisionInbox.load(from: [host("laptop")]).removing("laptop::late")
        XCTAssertEqual(inbox.decisions.map(\.sessionId), ["laptop::lead"])
    }
}
