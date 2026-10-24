import XCTest
@testable import Agenthail

final class TranscriptWindowTests: XCTestCase {
    private let ids = (0..<60).map { "row-\($0)" }

    func testOpensWithTheLatestRows() {
        let window = TranscriptWindow()
        XCTAssertEqual(window.start(in: ids), 60 - TranscriptWindow.opening)
        XCTAssertTrue(window.hidesOlder(in: ids))
        XCTAssertEqual(window.start(in: Array(ids.prefix(10))), 0)
        XCTAssertFalse(window.hidesOlder(in: Array(ids.prefix(10))))
    }

    func testRevealsOlderRowsAStepAtATimeUntilAllAreShown() {
        var window = TranscriptWindow()
        window.revealOlder(in: ids)
        XCTAssertEqual(window.start(in: ids), 60 - TranscriptWindow.opening - TranscriptWindow.step)
        window.revealOlder(in: ids)
        XCTAssertEqual(window.start(in: ids), 0)
        window.revealOlder(in: ids)
        XCTAssertEqual(window.start(in: ids), 0)
        XCTAssertFalse(window.hidesOlder(in: ids))
    }

    func testAPinnedWindowKeepsItsFirstRowAsRowsArrive() {
        var window = TranscriptWindow()
        let first = ids[window.start(in: ids)]
        window.pin(in: ids)
        let grown = ids + (60..<70).map { "row-\($0)" }
        XCTAssertEqual(grown[window.start(in: grown)], first)
        let loaded = (0..<20).map { "older-\($0)" } + grown
        XCTAssertEqual(loaded[window.start(in: loaded)], first)
        XCTAssertTrue(window.hidesOlder(in: loaded))
        window.pin(in: loaded)
        XCTAssertEqual(loaded[window.start(in: loaded)], first, "pinning again keeps the first pin")
    }

    func testAnUnpinnedWindowFollowsTheLatestRows() {
        let window = TranscriptWindow()
        let grown = ids + (60..<70).map { "row-\($0)" }
        XCTAssertEqual(window.start(in: grown), 70 - TranscriptWindow.opening)
    }

    func testRevealsThroughATargetRowAndNeverHidesShownRows() {
        var window = TranscriptWindow()
        window.revealThrough("row-5", in: ids)
        XCTAssertEqual(window.start(in: ids), 5)
        window.revealThrough("row-50", in: ids)
        XCTAssertEqual(window.start(in: ids), 5)
        window.revealThrough("missing", in: ids)
        XCTAssertEqual(window.start(in: ids), 5)
    }

    func testShowsTheRowsFromTheStart() {
        struct Row: Identifiable { let id: String }
        let rows = ids.map(Row.init)
        XCTAssertEqual(TranscriptWindow().shown(rows).map(\.id), Array(ids.suffix(TranscriptWindow.opening)))
    }
}
