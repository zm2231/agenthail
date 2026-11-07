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
}
