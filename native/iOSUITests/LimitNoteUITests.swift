import XCTest

@MainActor
final class LimitNoteUITests: XCTestCase {
    func testAStopWithANoteShowsTheNoteAsItsStatus() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "--preview-limit-note"]
        app.launch()
        let summary = app.buttons["session-summary"]
        XCTAssertTrue(summary.waitForExistence(timeout: 10))
        let note = "Its account has room again; restart it to continue"
        XCTAssertTrue(summary.label.contains(note), "the header's status is the stop's note: \(summary.label)")
        XCTAssertFalse(summary.label.contains("Usage limit"), "the generic label gives way to the note: \(summary.label)")
        let fragments = summary.staticTexts.allElementsBoundByIndex.filter { $0.frame.width > 0 && $0.frame.width < 20 }.map(\.label)
        XCTAssertTrue(fragments.isEmpty, "no text in the header row is squeezed to a sliver: \(fragments)")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "limit stop with a note"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
