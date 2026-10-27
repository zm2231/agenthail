import XCTest

@MainActor
final class LimitMoveUITests: XCTestCase {
    func testAStoppedSessionOffersTheAccountWithRoomAndAsksFirst() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-inspector", "--preview-limit-move"]
        app.launch()
        let details = app.collectionViews.firstMatch
        XCTAssertTrue(details.waitForExistence(timeout: 10))
        let move = app.buttons["Move to team@example.com"]
        for _ in 0..<4 where !(move.exists && move.isHittable) { details.swipeUp() }
        XCTAssertTrue(move.isHittable, "a stop with an offer shows the move")
        XCTAssertTrue(app.buttons["Resume now"].exists)
        let offer = XCTAttachment(screenshot: app.screenshot())
        offer.name = "limit stop with a move offer"
        offer.lifetime = .keepAlways
        add(offer)
        move.tap()
        XCTAssertTrue(app.staticTexts["Move this session to team@example.com?"].waitForExistence(timeout: 5), "the move asks first")
        XCTAssertTrue(app.staticTexts["It stops here and continues under that account. Chosen for most headroom, session 18%, week 40%."].exists)
        let question = XCTAttachment(screenshot: app.screenshot())
        question.name = "limit move confirmation"
        question.lifetime = .keepAlways
        add(question)
    }
}
