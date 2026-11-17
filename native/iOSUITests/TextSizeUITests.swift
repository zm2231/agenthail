import XCTest

@MainActor
final class TextSizeUITests: XCTestCase {
    func testTextSizeScalesTheTranscriptInBothThemes() {
        for theme in ["dark", "light"] {
            let app = XCUIApplication()
            app.launchArguments = ["--preview-session", "--preview-app", "--preview-theme", theme]
            app.launch()
            let normal = replyHeight(app, shot: "\(theme) 100%")
            setTextSize(app, steps: 4, expect: "140%")
            let large = replyHeight(app, shot: "\(theme) 140%")
            XCTAssertGreaterThan(large, normal * 1.2, "\(theme): the transcript's text grows with Text size")
            setTextSize(app, steps: -6, expect: "80%")
            let small = replyHeight(app, shot: "\(theme) 80%")
            XCTAssertLessThan(small, normal, "\(theme): the transcript's text shrinks with Text size")
            app.terminate()
        }
    }

    private func setTextSize(_ app: XCUIApplication, steps: Int, expect: String) {
        app.tab("Settings").tapWhenReady(timeout: 10)
        let stepper = app.steppers["text-size"]
        let list = app.collectionViews.firstMatch
        for _ in 0..<8 where !stepper.isHittable { list.swipeUp() }
        let button = app.buttons[steps > 0 ? "text-size-Increment" : "text-size-Decrement"]
        for _ in 0..<abs(steps) { button.tapWhenReady() }
        XCTAssertEqual(stepper.label, "Text size, \(expect)")
    }

    private func replyHeight(_ app: XCUIApplication, shot name: String) -> CGFloat {
        app.tab("Sessions").tapWhenReady(timeout: 10)
        let session = app.buttons["session-computer::demo"]
        if session.waitForExistence(timeout: 5) { session.tapWhenReady() }
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        let text = timeline.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'The pending build survives reconnecting'")).firstMatch
        XCTAssertTrue(text.waitForExistence(timeout: 5), "the transcript shows the reply")
        for _ in 0..<6 where !text.isHittable { timeline.swipeDown() }
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        let height = text.frame.height
        let back = app.navigationBars.buttons.firstMatch
        if back.exists { back.tap() }
        return height
    }
}
