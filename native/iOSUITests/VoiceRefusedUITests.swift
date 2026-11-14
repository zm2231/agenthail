import XCTest

@MainActor
final class VoiceRefusedUITests: XCTestCase {
    func testOpeningAndClosingVoiceWhileItsComputerRefusesConnectionsLeavesTheAppRunning() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry", "--preview-voice-refused"]
        app.launch()
        let voice = app.buttons["voice-entry"]
        XCTAssertTrue(voice.waitForExistence(timeout: 10))
        let rounds = Int(ProcessInfo.processInfo.environment["VOICE_REFUSED_ROUNDS"] ?? "") ?? 6
        for round in 1...rounds {
            voice.tapWhenReady()
            let failure = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "Reconnecting:")).firstMatch
            XCTAssertTrue(failure.waitForExistence(timeout: 10), "round \(round): the refused voice poll never failed on screen")
            _ = XCTWaiter.wait(for: [XCTestExpectation(description: "more refused polls")], timeout: Double(round % 3))
            app.buttons["Done"].tapWhenReady()
            XCTAssertTrue(voice.waitForExistence(timeout: 5), "round \(round): the app stopped after a refused voice poll")
            XCTAssertEqual(app.state, .runningForeground, "round \(round)")
        }
    }
}
