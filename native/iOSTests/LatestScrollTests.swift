import XCTest
@testable import Agenthail

final class LatestScrollTests: XCTestCase {
    func testATallTranscriptScrollsToItsLatestRow() {
        XCTAssertEqual(LatestScroll.target(overflows: true, latest: "q:computer/25", pinned: nil), .row("q:computer/25"))
        XCTAssertEqual(LatestScroll.target(overflows: true, latest: "q:computer/25", pinned: "g:earlier"), .row("q:computer/25"))
    }

    func testARepeatRequestForThePinnedRowGoesToTheEdge() {
        XCTAssertEqual(LatestScroll.target(overflows: true, latest: "q:computer/25", pinned: "q:computer/25"), .edge)
    }

    func testAShortTranscriptOrNoRowGoesToTheEdge() {
        XCTAssertEqual(LatestScroll.target(overflows: false, latest: "q:computer/25", pinned: nil), .edge)
        XCTAssertEqual(LatestScroll.target(overflows: true, latest: nil, pinned: nil), .edge)
    }

    private func item(_ id: String, kind: String = "message", role: String? = "assistant", turn: String?) -> TimelineItem {
        TimelineItem(id: id, kind: kind, role: role, title: "", text: id, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil, turnId: turn)
    }

    func testANotificationOpensTheRowHoldingItsTurnsLastReply() {
        let items = [
            item("p1", role: "user", turn: "t1"), item("r1", turn: "t1"),
            item("p2", role: "user", turn: "t2"), item("c2", kind: "toolCall", role: nil, turn: "t2"), item("r2a", turn: "t2"), item("r2b", turn: "t2"),
            item("p3", role: "user", turn: "t3"), item("c3", kind: "toolCall", role: nil, turn: "t3"),
        ]
        XCTAssertEqual(TurnReveal.item("t2", in: items), "r2b", "the turn's last reply, not its first")
        XCTAssertEqual(TurnReveal.item("t3", in: items), "c3", "a turn with no reply opens at its last row")
        XCTAssertNil(TurnReveal.item("t9", in: items), "a turn outside the loaded rows is not found")
        let groups = [TimelineGroup(id: "g:p1", items: Array(items[0...1])), TimelineGroup(id: "g:p2", items: Array(items[2...3])), TimelineGroup(id: "g:r2", items: Array(items[4...5]))]
        XCTAssertEqual(TurnReveal.row("t2", groups: groups), "g:r2", "the reply's row, through the group that folds it")
    }

    func testANotificationCarriesItsTurnWhenItHasOne() {
        XCTAssertEqual(IOSAppDelegate.openedTurn(["sessionId": "s", "host": "h", "turnId": "turn-42"]), "turn-42")
        XCTAssertNil(IOSAppDelegate.openedTurn(["sessionId": "s", "host": "h"]))
        XCTAssertNil(IOSAppDelegate.openedTurn(["sessionId": "s", "host": "h", "turnId": ""]))
    }
}
