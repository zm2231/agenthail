import XCTest

@MainActor
final class VoiceSceneUITests: XCTestCase {
    func testFailedVoicePollsDoNotReevaluateTheAppScene() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry", "--preview-voice-unreachable"]
        app.launch()
        let voice = app.buttons["voice-entry"]
        XCTAssertTrue(voice.waitForExistence(timeout: 10))
        let stamp = app.staticTexts["scene-evaluations"]
        XCTAssertTrue(stamp.waitForExistence(timeout: 5))
        let before = Int(stamp.label) ?? -1

        voice.tapWhenReady()
        let failure = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "Reconnecting:")).firstMatch
        XCTAssertTrue(failure.waitForExistence(timeout: 10), "the unreachable voice host never reported a failed poll")
        _ = XCTWaiter.wait(for: [XCTestExpectation(description: "several more failed polls")], timeout: 5)
        app.buttons["Done"].tapWhenReady()
        XCTAssertTrue(voice.waitForExistence(timeout: 5))

        let after = Int(stamp.label) ?? -1
        XCTAssertGreaterThan(before, 0)
        XCTAssertLessThanOrEqual(after - before, 1, "each failed voice poll re-evaluated the whole app scene")
    }
}
