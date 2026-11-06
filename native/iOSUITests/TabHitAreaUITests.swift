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
        let tabs = ["Sessions", "Inbox", "Settings"].map { app.tab($0) }
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
            let other = tab == tabs[0] ? tabs[1] : tabs[0]
            func answers(_ dx: Double, _ dy: Double) -> Bool {
                other.tapWhenReady()
                tap(window, at: tab.frame, dx: dx, dy: dy)
                return tab.isSelected
            }
            for dx in [-22.0, 22.0] { XCTAssertTrue(answers(dx, 0), "\(tab.label) misses a tap at \(dx), 0") }
            if app.isPad {
                // The top tab bar sits under the status bar, so its 44 pt band can start below the frame's centre line.
                let band = stride(from: -27.0, through: 27.0, by: 3.0).filter { answers(0, $0) }
                XCTAssertGreaterThanOrEqual((band.last ?? 0) - (band.first ?? 0), 42, "\(tab.label) answers taps across only \(band.first ?? 0)...\(band.last ?? 0) pt")
            } else {
                for dy in [-21.0, 21.0] { XCTAssertTrue(answers(0, dy), "\(tab.label) misses a tap at 0, \(dy)") }
            }
        }
    }

    private func tap(_ window: XCUIElement, at frame: CGRect, dx: Double, dy: Double) {
        window.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: frame.midX + dx, dy: frame.midY + dy))
            .tap()
    }
}
