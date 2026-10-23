import XCTest

/// Toolbar items draw at 36 pt on iOS 26, so their accessibility frames are
/// smaller than the area that takes a tap. Probing the voice button with AXe
/// taps a point at a time on an iPhone 17 Pro simulator found it answers across
/// the navigation bar row, y 63-106 and x 16-70: 44 pt tall and 54 pt wide, at
/// the default and the largest text size. These tests tap near those edges and
/// near the edges of a 44 pt square on each tab, and check the right target answers.
@MainActor
final class TabHitAreaUITests: XCTestCase {
    func testTabsAndVoiceTakeTapsAcrossAFullSizeTarget() {
        check(textSize: nil)
    }

    func testTabsAndVoiceTakeTapsAtTheLargestTextSize() {
        check(textSize: "UICTContentSizeCategoryAccessibilityXXXL")
    }

    private func check(textSize: String?) {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry"]
        if let textSize { app.launchArguments += ["-UIPreferredContentSizeCategoryName", textSize] }
        app.launch()
        let voice = app.buttons["voice-entry"]
        XCTAssertTrue(voice.waitForExistence(timeout: 10))
        let tabs = ["Sessions", "Inbox", "Settings"].map { app.tabBars.buttons[$0] }
        for tab in tabs { XCTAssertTrue(tab.waitForExistence(timeout: 5), tab.label) }
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "hit areas \(textSize ?? "default")"
        shot.lifetime = .keepAlways
        add(shot)

        for tab in tabs {
            XCTAssertFalse(voice.frame.intersects(tab.frame), "voice overlaps \(tab.label)")
        }
        let window = app.windows.firstMatch
        for (dx, dy) in [(0.0, -21.0), (0.0, 21.0), (-25.0, 0.0), (25.0, 0.0)] {
            tap(window, at: voice.frame, dx: dx, dy: dy)
            XCTAssertTrue(app.navigationBars["Talk to orchestrator"].waitForExistence(timeout: 5), "voice misses a tap at \(dx), \(dy)")
            app.buttons["Done"].tapWhenReady()
            XCTAssertTrue(voice.waitForExistence(timeout: 5))
        }
        for tab in tabs.reversed() {
            for (dx, dy) in [(-22.0, 0.0), (22.0, 0.0), (0.0, -21.0), (0.0, 21.0)] {
                tap(window, at: tab.frame, dx: dx, dy: dy)
                XCTAssertTrue(tab.isSelected, "\(tab.label) misses a tap at \(dx), \(dy)")
                if tab.label != "Sessions" {
                    tabs[0].tapWhenReady()
                }
            }
        }
    }

    private func tap(_ window: XCUIElement, at frame: CGRect, dx: Double, dy: Double) {
        window.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: frame.midX + dx, dy: frame.midY + dy))
            .tap()
    }
}
