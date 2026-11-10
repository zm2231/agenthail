import XCTest
@testable import Agenthail

@MainActor
final class FinishedUnseenTests: XCTestCase {
    func testASessionThatFinishesOffScreenRingsUntilItIsOpened() throws {
        let model = makeModel()
        try show(model, status: "busy")
        XCTAssertEqual(ring(model), .working)
        try show(model, status: "idle")
        XCTAssertTrue(model.finishedUnseen.contains("computer::s1"))
        XCTAssertEqual(ring(model), .finished)
        XCTAssertEqual(SessionRing.finished.spoken, "Finished")

        model.sessionAppeared("computer::s1")
        XCTAssertFalse(model.finishedUnseen.contains("computer::s1"), "opening the session marks it seen")
        XCTAssertEqual(ring(model), .idle)
        model.sessionDisappeared("computer::s1")
    }

    func testASessionThatFinishesOnScreenIsNeverMarked() throws {
        let model = makeModel()
        try show(model, status: "busy")
        model.sessionAppeared("computer::s1")
        try show(model, status: "idle")
        XCTAssertFalse(model.finishedUnseen.contains("computer::s1"))
        model.sessionDisappeared("computer::s1")

        try show(model, status: "busy")
        try show(model, status: "idle")
        XCTAssertTrue(model.finishedUnseen.contains("computer::s1"), "after leaving the screen the next finish rings again")
    }

    func testWorkingAgainClearsTheFinishedRing() throws {
        let model = makeModel()
        try show(model, status: "busy")
        try show(model, status: "idle")
        try show(model, status: "busy")
        XCTAssertFalse(model.finishedUnseen.contains("computer::s1"))
        XCTAssertEqual(ring(model), .working)
    }

    func testANeedsYouOrUnavailableRingOutranksFinished() throws {
        let model = makeModel()
        try show(model, status: "busy")
        try show(model, status: "idle")
        let session = try XCTUnwrap(model.session("computer::s1"))
        XCTAssertEqual(SessionRing.kind(session, needsYou: true, finished: true), .needsYou)
    }

    private func ring(_ model: AgenthailIOSModel) -> SessionRing? {
        model.session("computer::s1").map { SessionRing.kind($0, needsYou: false, finished: model.finishedUnseen.contains($0.id)) }
    }

    private func show(_ model: AgenthailIOSModel, status: String) throws {
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"\#(status)","daemon":{"running":true},"surfaces":[],"sessions":[{"id":"s1","surface":"codex","name":"task","status":"\#(status)","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}}],"totalSessions":1,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24}"#.utf8)))
    }

    private func makeModel() -> AgenthailIOSModel {
        AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://fixture.invalid")!, token: "fixture", session: FixtureScope.open(self).session([UnavailableProtocol.self])))
    }
}

private final class UnavailableProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        served {
            self.client?.urlProtocol(self, didReceive: HTTPURLResponse(url: self.request.url!, statusCode: 503, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
            self.client?.urlProtocol(self, didLoad: Data(#"{"error":{"message":"fixture unavailable"}}"#.utf8))
            self.client?.urlProtocolDidFinishLoading(self)
        }
    }
    override func stopLoading() {}
}
