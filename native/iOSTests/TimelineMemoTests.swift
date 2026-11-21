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

    func testFocusFoldsTheTurnsWorkIntoOneLineBeforeItsLastReplyAndFollowsTheSetting() {
        let memo = TimelineMemo()
        let call = TimelineItem(id: "call", kind: "toolCall", role: nil, title: "Bash", text: "ls", timestamp: nil, callId: "k", status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        let late = TimelineItem(id: "late", kind: "toolCall", role: nil, title: "Bash", text: "pwd", timestamp: nil, callId: "l", status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        let rows = [item("a"), call, item("b"), late]
        let focused = memo.groups(latest: rows, older: [], foldAgents: true, focus: true)
        XCTAssertEqual(focused.flatMap(\.items).map(\.id), ["a", "call", "late", "b"], "the turn's work gathers just before its last reply, which reads last")
        let work = focused.first { $0.items.contains { $0.id == "call" } }
        XCTAssertEqual(work?.focus, true)
        XCTAssertEqual(work?.folds, true, "the work is one folded line")
        XCTAssertEqual(memo.groups(latest: rows, older: [], foldAgents: true, focus: false).flatMap(\.items).map(\.id), ["a", "call", "b", "late"], "groups ignored turning focus off")
    }
}
