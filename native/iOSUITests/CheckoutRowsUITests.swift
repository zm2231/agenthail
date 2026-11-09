import XCTest

@MainActor
final class CheckoutRowsUITests: XCTestCase {
    func testEachWorktreeIsNamedWithItsBranchAndUncommittedChanges() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-checkouts", "--preview-theme", "dark"]
        app.launch()
        app.tab("Sessions").tapWhenReady()
        let main = app.descendants(matching: .any)["checkout-computer::checkout-main"]
        let fix = app.descendants(matching: .any)["checkout-computer::checkout-fix"]
        XCTAssertTrue(main.waitForExistence(timeout: 10))
        XCTAssertEqual(main.label, "Worktree main")
        XCTAssertTrue(fix.exists)
        XCTAssertEqual(fix.label, "Worktree fix-login, uncommitted changes")
        XCTAssertFalse(app.descendants(matching: .any)["checkout-computer::checkout-agenthail"].exists, "a single checkout has no row of its own")
        let agenthail = app.descendants(matching: .any)["project-computer::project-agenthail"]
        for _ in 0..<3 where !agenthail.exists { app.swipeUp() }
        XCTAssertTrue(agenthail.exists)
        XCTAssertTrue(agenthail.label.hasSuffix(", branch main, uncommitted changes"), "the single checkout's branch and changes are in its project header: \(agenthail.label)")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "checkout rows"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
