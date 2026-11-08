import XCTest

@MainActor
final class ReturnAndTypingUITests: XCTestCase {
    private let latest = "Every target builds after reconnecting."

    func testTheTranscriptStaysDrawnThroughLongTypingReturningAndNotificationCenter() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-long-activity", "--preview-theme", "dark"]
        app.launch()
        let reply = app.staticTexts[latest]
        XCTAssertTrue(reply.waitForExistence(timeout: 15), "the session opens at its latest reply")

        let composer = app.textViews["composer-input"]
        XCTAssertTrue(composer.waitForExistence(timeout: 5))
        composer.tap()
        let typed = String(repeating: "0123456789", count: 100)
        let draft = (composer.value as? String ?? "").count + 2000
        let keepsDraft = { (step: String) in
            XCTAssertEqual((composer.value as? String)?.count, draft, "the draft keeps every character \(step)")
        }
        let timeline = app.scrollViews["session-timeline"]
        let band = {
            let top = app.navigationBars.firstMatch.frame.maxY
            let bottom = app.otherElements["composer"].frame.minY
            return CGRect(x: 0, y: top, width: app.windows.firstMatch.frame.width, height: max(0, bottom - top))
        }
        let visibleRows = { (try? timeline.snapshot())?.textCount(in: band()) ?? 0 }
        let drawn = { (step: String) in
            let deadline = Date().addingTimeInterval(2)
            var rows = visibleRows()
            while rows == 0 && Date() < deadline {
                RunLoop.current.run(until: Date().addingTimeInterval(0.05))
                rows = visibleRows()
            }
            let keyboard = app.keyboards.firstMatch
            XCTAssertGreaterThan(rows, 0, "the transcript shows rows between the top bar and the composer within 2 s \(step): band \(band()), keyboard \(keyboard.exists ? "\(keyboard.frame)" : "hidden")")
        }
        let started = Date()
        composer.typeText(typed)
        composer.typeText(typed)
        let typing = Date().timeIntervalSince(started)
        XCTAssertLessThan(typing, 90, "typing 2,000 characters took \(Int(typing)) s; the harness alone takes about 35 s")
        keepsDraft("after typing")
        drawn("of typing 2,000 characters")

        XCUIDevice.shared.press(.home)
        XCTAssertTrue(app.wait(for: .runningBackground, timeout: 10))
        app.activate()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10))
        drawn("of returning to the app")
        keepsDraft("after returning to the app")

        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for pull in 1...2 {
            let top = springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.2, dy: 0.001))
            top.press(forDuration: 0.1, thenDragTo: springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.2, dy: 0.7)))
            let bottom = springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.98))
            bottom.press(forDuration: 0.1, thenDragTo: springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2)))
            XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10))
            drawn("of closing Notification Center (pull \(pull))")
            keepsDraft("after Notification Center (pull \(pull))")
        }
    }
}

private extension XCUIElementSnapshot {
    func textCount(in band: CGRect) -> Int {
        let shown = elementType == .staticText && band.intersection(frame).height >= 10 ? 1 : 0
        return children.reduce(shown) { $0 + $1.textCount(in: band) }
    }
}
