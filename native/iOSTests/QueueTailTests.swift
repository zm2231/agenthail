import XCTest
@testable import Agenthail

final class QueueTailTests: XCTestCase {
    private let start = Date(timeIntervalSince1970: 1_800_000_000)

    private func row(_ id: Int64, status: String = "pending", message: String = "m", author: String? = "person", operation: String? = nil, lastError: String? = nil) throws -> QueueState {
        var object: [String: Any] = ["id": id, "sessionId": "s", "target": "t", "message": message, "status": status, "attempts": 0, "queuedAt": "2026-11-04T10:00:00.000000000Z", "evidence": "queued"]
        if let author { object["author"] = author }
        if let lastError { object["lastError"] = lastError }
        if let operation { object["operation"] = operation }
        var item = try JSONDecoder().decode(QueueState.self, from: JSONSerialization.data(withJSONObject: object))
        item.hostID = "computer"
        return item
    }

    private func user(_ text: String, at timestamp: String = "2026-11-04T10:00:30Z") throws -> TimelineItem {
        let object: [String: Any] = ["id": UUID().uuidString, "kind": "message", "role": "user", "title": "You", "text": text, "timestamp": timestamp, "truncated": false]
        return try JSONDecoder().decode(TimelineItem.self, from: JSONSerialization.data(withJSONObject: object))
    }

    private func envelope(_ body: String) -> String {
        #"<cross-session-message from="uds:/tmp/a.sock" from-name="agenthail/claude: @helper">"# + body + "</cross-session-message>"
    }

    func testAWaitingAndAFailedRowShowOthersDont() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1), row(2, status: "inflight"), row(3, status: "dead"), row(4, status: "delivered"), row(5, status: "canceled"), row(6, operation: "goal-set")], timeline: [], now: start)
        XCTAssertEqual(tail.rows.map(\.item.id), [1, 2, 3])
    }

    func testADeliveredRowStaysUntilItsTimelineRowArrivesWithoutADuplicate() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "go")], timeline: [], now: start)
        tail.update(active: [], timeline: [], now: start.addingTimeInterval(1))
        XCTAssertEqual(tail.rows.map(\.item.id), [1], "no gap between the queue row and the timeline row")
        XCTAssertNotNil(tail.rows.first?.leavingSince)
        try tail.update(active: [], timeline: [user("go")], now: start.addingTimeInterval(2))
        XCTAssertTrue(tail.rows.isEmpty, "no duplicate once the timeline has it")
    }

    func testTheTimelineArrivingBeforeTheQueueUpdatesShowsNoDuplicate() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "go")], timeline: [user("go")], now: start)
        XCTAssertTrue(tail.rows.isEmpty)
    }

    func testARowThatNeverMatchesLeavesOnTheClock() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "go")], timeline: [], now: start)
        tail.update(active: [], timeline: [], now: start)
        XCTAssertEqual(tail.nextDeparture, start.addingTimeInterval(QueueTail.leavesAfter))
        tail.update(active: [], timeline: [], now: start.addingTimeInterval(QueueTail.leavesAfter - 1))
        XCTAssertEqual(tail.rows.count, 1)
        tail.update(active: [], timeline: [], now: start.addingTimeInterval(QueueTail.leavesAfter))
        XCTAssertTrue(tail.rows.isEmpty, "a row can't stay stuck")
    }

    func testAnEnvelopedAgentMessageMatchesItsTimelineRow() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: envelope("ready"), author: "agent")], timeline: [], now: start)
        tail.update(active: [], timeline: [], now: start.addingTimeInterval(1))
        try tail.update(active: [], timeline: [user(envelope("ready"))], now: start.addingTimeInterval(2))
        XCTAssertTrue(tail.rows.isEmpty)
    }

    func testAnOlderIdenticalMessageIsNotTheDeliveredRow() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "go")], timeline: [user("go", at: "2026-11-04T09:00:00Z")], now: start)
        XCTAssertEqual(tail.rows.count, 1)
    }

    func testOneTimelineRowClearsOneOfTwoIdenticalMessages() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "go"), row(2, message: "go")], timeline: [user("go")], now: start)
        XCTAssertEqual(tail.rows.map(\.item.id), [2])
    }

    func testAFailedRowStaysEvenIfTheTimelineMatchesIt() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, status: "dead", message: "go")], timeline: [user("go")], now: start.addingTimeInterval(600))
        XCTAssertEqual(tail.rows.map(\.item.id), [1], "an unknown outcome stays up for review")
        XCTAssertNil(tail.nextDeparture)
    }

    func testAFreshTailAfterReopeningHoldsNoLeavingRow() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1)], timeline: [], now: start)
        tail.update(active: [], timeline: [], now: start)
        XCTAssertFalse(tail.rows.isEmpty)
        tail = QueueTail()
        tail.update(active: [], timeline: [], now: start)
        XCTAssertTrue(tail.rows.isEmpty)
    }

    func testADroppedRowStaysOutWhileAStaleListStillHasIt() throws {
        var tail = QueueTail()
        let item = try row(1)
        tail.update(active: [item], timeline: [], now: start)
        tail.drop(try XCTUnwrap(tail.rows.first))
        XCTAssertTrue(tail.rows.isEmpty)
        tail.update(active: [item], timeline: [], now: start)
        XCTAssertTrue(tail.rows.isEmpty, "it doesn't come back from a stale list")
        tail.update(active: [], timeline: [], now: start)
        XCTAssertTrue(tail.rows.isEmpty, "and doesn't come back as leaving")
    }

    func testASteeredRowStaysUntilTheModelTakesIt() throws {
        var tail = QueueTail()
        let item = try row(1, message: "go")
        tail.update(active: [item], timeline: [], turnRunning: true, now: start)
        tail.steered(try XCTUnwrap(tail.rows.first), now: start)
        tail.update(active: [item], timeline: [], turnRunning: true, now: start.addingTimeInterval(1))
        XCTAssertEqual(tail.rows.map(\.item.id), [1], "a stale list doesn't make it a queued row again")
        XCTAssertNotNil(tail.rows.first?.leavingSince)
        tail.update(active: [], timeline: [], turnRunning: true, now: start.addingTimeInterval(1800))
        XCTAssertEqual(tail.rows.count, 1, "a long tool run can hold the next model request for many minutes")
        try tail.update(active: [], timeline: [user("go")], turnRunning: true, now: start.addingTimeInterval(1801))
        XCTAssertTrue(tail.rows.isEmpty, "no duplicate once the timeline has it")
    }

    func testASteeredRowLeavesAfterTheTurnEndsIfNothingMatches() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "go")], timeline: [], turnRunning: true, now: start)
        tail.steered(try XCTUnwrap(tail.rows.first), now: start)
        let ended = start.addingTimeInterval(600)
        tail.update(active: [], timeline: [], turnRunning: true, now: ended.addingTimeInterval(-5))
        tail.update(active: [], timeline: [], turnRunning: false, now: ended)
        XCTAssertEqual(tail.nextDeparture, ended.addingTimeInterval(QueueTail.leavesAfter), "the clock starts when the turn ends")
        tail.update(active: [], timeline: [], turnRunning: false, now: ended.addingTimeInterval(QueueTail.leavesAfter - 1))
        XCTAssertEqual(tail.rows.count, 1)
        tail.update(active: [], timeline: [], turnRunning: false, now: ended.addingTimeInterval(QueueTail.leavesAfter))
        XCTAssertTrue(tail.rows.isEmpty, "it can't stay stuck once the turn is over")
    }

    func testASteerFinishingAfterTheTurnEndedLeavesEightSecondsLater() throws {
        var tail = QueueTail()
        let item = try row(1, message: "go")
        tail.update(active: [item], timeline: [], turnRunning: false, now: start)
        tail.steered(try XCTUnwrap(tail.rows.first), now: start)
        tail.update(active: [], timeline: [], turnRunning: false, now: start.addingTimeInterval(QueueTail.leavesAfter - 1))
        XCTAssertEqual(tail.rows.count, 1)
        tail.update(active: [], timeline: [], turnRunning: false, now: start.addingTimeInterval(QueueTail.leavesAfter))
        XCTAssertTrue(tail.rows.isEmpty, "the clock isn't started a second time")
    }

    func testAPlainDeliveredRowKeepsItsShortClockWhileATurnRuns() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "go")], timeline: [], turnRunning: true, now: start)
        tail.update(active: [], timeline: [], turnRunning: true, now: start.addingTimeInterval(1))
        tail.update(active: [], timeline: [], turnRunning: true, now: start.addingTimeInterval(1 + QueueTail.leavesAfter))
        XCTAssertTrue(tail.rows.isEmpty)
    }

    func testAPeerIsNamedTheWayTheDaemonNamesIt() {
        for (sender, name) in [("agenthail/claude: @helper", "@helper"), ("agenthail/claude: worker", "worker"), ("agenthail/codex: Fix the build", "Fix the build"), ("uds:/tmp/a.sock", "uds:/tmp/a.sock"), ("agenthail/claude: ", "agenthail/claude:")] {
            XCTAssertEqual(PeerEnvelope(sender: sender, body: "m").senderName, name)
        }
    }

    func testAnActionFailureFromTheLastSessionDoesNotShowOnTheNextOne() {
        var tail = QueueTail()
        let earlier = tail.generation
        tail.report("failed", from: earlier)
        XCTAssertEqual(tail.problem, "failed")
        tail = QueueTail()
        tail.report("late failure", from: earlier)
        XCTAssertNil(tail.problem)
    }

    func testRowsReadOldestToNewestWhicheverRanksKeptThem() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "mine"), row(2, message: envelope("a"), author: "agent"), row(3, status: "dead")], timeline: [], now: start)
        XCTAssertEqual(tail.shown.map(\.item.id), [1, 2, 3], "the transcript stays in delivery order")
        XCTAssertEqual(tail.lastShownID, tail.shown.last?.id)
    }

    func testTheCapShowsThreeWithTheRestCounted() throws {
        var tail = QueueTail()
        try tail.update(active: (1...5).map { try row(Int64($0)) }, timeline: [], now: start)
        XCTAssertEqual(tail.shown.map(\.item.id), [3, 4, 5])
        XCTAssertEqual(tail.hiddenCount, 2)
        XCTAssertEqual(tail.lastShownID, TranscriptRowID.queue("computer/5"))
        tail.expanded = true
        XCTAssertEqual(tail.shown.count, 5)
        XCTAssertEqual(tail.hiddenCount, 0)
    }

    func testPeerRowsCountButDontOutrankThePersonsOwn() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, message: "mine"), row(2, message: envelope("a"), author: "agent"), row(3, message: envelope("b"), author: "agent"), row(4, message: envelope("c"), author: "agent"), row(5, message: envelope("d"), author: "agent")], timeline: [], now: start)
        XCTAssertEqual(tail.shown.map(\.item.id), [1, 4, 5])
        XCTAssertEqual(tail.hiddenCount, 2)
    }

    func testAFailedRowOffersReviewAndSaysItNeedsIt() throws {
        let dead = QueueTail.Row(item: try row(1, status: "dead"))
        XCTAssertEqual(dead.controls, .review, "a failed row offers Send again and Dismiss")
        XCTAssertEqual(dead.heading, "Delivery needs review")
        XCTAssertEqual(dead.reason, "It may already have reached the agent. Check the session before sending again.")
        let failed = try row(2, status: "dead", lastError: "delivery outcome is unknown: the connection dropped")
        XCTAssertEqual(QueueTail.Row(item: failed).reason, "delivery outcome is unknown: the connection dropped")
        XCTAssertNil(QueueTail.Row(item: try row(3)).reason, "a waiting row gives no reason")
    }

    func testOnlyAWaitingRowOffersSteerAndItsMenu() throws {
        XCTAssertEqual(QueueTail.Row(item: try row(1)).controls, .waiting)
        XCTAssertEqual(QueueTail.Row(item: try row(1)).heading, "Queued")
        let delivering = QueueTail.Row(item: try row(2, status: "inflight"))
        XCTAssertEqual(delivering.controls, QueueTail.Row.Controls.none)
        XCTAssertEqual(delivering.heading, "Delivering")
        let leaving = QueueTail.Row(item: try row(3, status: "dead"), leavingSince: start)
        XCTAssertEqual(leaving.controls, QueueTail.Row.Controls.none, "a row on its way out offers nothing")
        XCTAssertEqual(leaving.heading, "Delivering")
    }

    func testAPeerRowIsHeadedByItsSender() throws {
        let peer = QueueTail.Row(item: try row(1, message: envelope("Check the release notes."), author: "agent"))
        XCTAssertEqual(peer.heading, "Queued · From @helper")
        XCTAssertFalse(peer.isOwn)
    }

    func testAFailedRowOutranksWaitingRows() throws {
        var tail = QueueTail()
        try tail.update(active: [row(1, status: "dead"), row(2), row(3), row(4), row(5)], timeline: [], now: start)
        XCTAssertEqual(tail.shown.map(\.item.id), [1, 4, 5])
    }
}
