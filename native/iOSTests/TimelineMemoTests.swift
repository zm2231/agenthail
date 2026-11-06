import XCTest
@testable import Agenthail

@MainActor
final class TimelineMemoTests: XCTestCase {
    private func item(_ id: String, _ text: String? = nil) -> TimelineItem {
        TimelineItem(id: id, kind: "message", role: "assistant", title: "assistant", text: text ?? id, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
    }

    func testOlderRowsComeFirstAndALatestRowIsNotRepeated() {
        let memo = TimelineMemo()
        let items = memo.items(latest: [item("b"), item("c")], older: [item("a"), item("b")])
        XCTAssertEqual(items.map(\.id), ["a", "b", "c"])
    }

    func testGroupsFollowEachChangeInTheRowsAndTheFold() {
        let memo = TimelineMemo()
        let first = [item("a"), item("b")]
        XCTAssertEqual(memo.groups(latest: first, older: [], foldAgents: true).map(\.id), TimelineGroup.make(first, foldAgents: true).map(\.id))
        let grown = first + [item("c")]
        XCTAssertEqual(memo.groups(latest: grown, older: [], foldAgents: true).map(\.id), TimelineGroup.make(grown, foldAgents: true).map(\.id), "groups kept the rows from before the change")
        let edited = [item("a"), item("b"), item("c", "c edited")]
        XCTAssertEqual(memo.groups(latest: edited, older: [], foldAgents: true).flatMap(\.items).last?.text, "c edited", "groups kept a row's old text")
        XCTAssertEqual(memo.groups(latest: edited, older: [], foldAgents: false).map(\.id), TimelineGroup.make(edited, foldAgents: false).map(\.id), "groups ignored the fold setting")
        XCTAssertEqual(memo.items(latest: [item("z")], older: [item("y")]).map(\.id), ["y", "z"])
        XCTAssertEqual(memo.groups(latest: [item("z")], older: [item("y")], foldAgents: false).flatMap(\.items).map(\.id), ["y", "z"], "groups kept rows after the items changed")
    }
}
