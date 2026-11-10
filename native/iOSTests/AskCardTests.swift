import XCTest
@testable import Agenthail

@MainActor
final class AskCardTests: XCTestCase {
    private func row(respond: Bool = false, ask: String) throws -> SessionState {
        let json = #"{"id":"s1","surface":"codex","name":"builder","alias":"builder","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false,"respond":"# + "\(respond)" + #"},"endTurn":{"headline":"Waiting on the deploy call","endedAt":"2026-11-06T04:52:30Z"},"ask":"# + ask + "}"
        return try JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
    }

    func testRowDecodesTheWaitingAskAndKeysOnIt() throws {
        let waiting = try row(ask: #"{"id":"ask-000000000001","title":"Ready to ship","routedTo":"person","askedAt":"2026-11-06T04:52:30Z"}"#)
        XCTAssertEqual(waiting.pendingAskID, "ask-000000000001")
        XCTAssertTrue(RequestSync.lists(waiting), "an ask is listed though the provider answers nothing through Agenthail")
        let newer = try row(ask: #"{"id":"ask-000000000002","title":"Next","routedTo":"person","askedAt":"2026-11-06T05:00:00Z"}"#)
        XCTAssertNotEqual(RequestSync.cacheKey(waiting), RequestSync.cacheKey(newer), "a new ask refetches")
        let answered = try row(ask: #"{"id":"ask-000000000001","title":"Ready to ship","routedTo":"person","askedAt":"2026-11-06T04:52:30Z","answeredAt":"2026-11-06T04:55:02Z"}"#)
        XCTAssertNil(answered.pendingAskID)
        XCTAssertFalse(RequestSync.lists(answered), "an answered ask is not fetched")
        let routed = try row(ask: #"{"id":"ask-000000000003","title":"Ready","routedTo":"lead-session","askedAt":"2026-11-06T04:52:30Z"}"#)
        XCTAssertNil(routed.pendingAskID, "an ask routed to another session is not the person's")
    }

    func testRequestDecodesAndIsNeverOfferedOnceAnswered() throws {
        let json = #"{"permissionMode":"","requests":[{"id":"ask-000000000001","kind":"ask","tool":"","title":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"id":"o0","label":"Deploy","description":""},{"id":"o1","label":"Wait","description":"hold until Monday"}],"suggested":0,"links":[{"title":"Runbook","url":"https://example.com"}]}],"actions":[{"id":"o0","title":"Deploy"}],"alwaysAllow":false,"createdAt":"2026-11-06T04:52:30Z"}]}"#
        let requests = try JSONDecoder().decode(SessionRequests.self, from: Data(json.utf8))
        let ask = try XCTUnwrap(requests.requests.first)
        XCTAssertTrue(ask.isAsk && ask.usesPager)
        XCTAssertEqual(ask.questions?.first?.suggested, 0)
        XCTAssertEqual(ask.questions?.first?.links, [SessionRequest.Link(title: "Runbook", url: "https://example.com")])
        XCTAssertEqual(RequestCard.heading(ask, asker: "@builder"), "@builder asks: Ready to ship")
        XCTAssertEqual(requests.offered(pendingAsk: "ask-000000000001").map(\.id), ["ask-000000000001"])
        XCTAssertTrue(requests.offered(pendingAsk: nil).isEmpty, "an answered ask is never offered")
        XCTAssertTrue(requests.offered(pendingAsk: "ask-000000000001", retired: "ask-000000000001").isEmpty, "an ask answered here is not offered while the row catches up")
    }

    func testPagerWaitsForSubmitAndSendsTheNote() throws {
        let question = SessionRequest.Question(question: "Deploy it now?", header: "", multiSelect: false, options: [.init(id: "o0", label: "Deploy", description: ""), .init(id: "o1", label: "Wait", description: "")])
        var pager = QuestionPager(questions: [question], ask: true)
        XCTAssertNil(pager.pick(1), "a pick never sends an ask")
        pager.note = " After lunch. "
        XCTAssertEqual(try XCTUnwrap(pager.submit()), RequestDecision(choices: ["o1"], message: "After lunch."))
    }

    func testFailuresDropTheCardOnlyWhenTheAnswerMayHaveLanded() {
        XCTAssertTrue(RequestAnswerFailure(AgenthailAPIError.requestStale("x")).dropsCard)
        XCTAssertTrue(RequestAnswerFailure(AgenthailAPIError.request(502, "the answer may have reached the session, so it is not sent again: x")).dropsCard)
        XCTAssertFalse(RequestAnswerFailure(AgenthailAPIError.request(502, "session is gone")).dropsCard)
    }

    func testEveryWaitingPreviewDetailCarriesItsAttentionAtTheRoot() throws {
        let cases: [(String, [String: String])] = [("--preview-terminal-question", SessionPreview.terminalAttention), ("--preview-questions", SessionPreview.questionsAttention), ("--preview-unreachable", SessionPreview.unreachableAttention)]
        for (argument, attention) in cases {
            let detail = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.previewDetail(arguments: [argument]).utf8))
            XCTAssertNotNil(detail.session.waitingOn, argument)
            XCTAssertEqual(detail.sessionState(open: true).attention, RowAttention(kind: attention["kind"]!, reason: attention["reason"]!), argument)
        }
        let plain = try JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.previewDetail(arguments: []).utf8))
        XCTAssertNil(plain.attention, "a session that waits on nothing carries no attention")
    }
}
