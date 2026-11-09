import XCTest

@MainActor
final class LivePushSetupUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testAPushSetupThatKeepsFailingOnReconnectStaysOutOfTheWayAndShowsInSettings() throws {
        let control = try LiveControl()
        var appAlerts: [String] = []
        addUIInterruptionMonitor(withDescription: "alerts") { alert in
            if alert.label == "Agenthail" {
                appAlerts.append(alert.staticTexts.allElementsBoundByIndex.map(\.label).joined(separator: " | "))
                alert.buttons.firstMatch.tap()
            } else if alert.buttons["Allow"].exists {
                alert.buttons["Allow"].tap()
            } else {
                alert.buttons.firstMatch.tap()
            }
            return true
        }
        let app = launchForLiveHosts()
        _ = try pairLiveHost(0, control: control, in: app)
        let host = try pairLiveHost(1, control: control, in: app)
        let row = liveRow("Check the release notes", host: host, in: app).firstMatch
        requireLive(row.waitForExistence(timeout: 30), "the host's conversation is listed", app)

        for cycle in 1...2 {
            XCUIDevice.shared.press(.home)
            sleep(5)
            app.activate()
            requireLive(app.wait(for: .runningForeground, timeout: 20), "the app comes back in cycle \(cycle)", app)
            try control.stop(host: 1)
            sleep(10)
            try control.start(host: 1)
            app.tab("Sessions").tapWhenReady(timeout: 20)
            requireLive(row.waitForExistence(timeout: 60), "the host is back in cycle \(cycle)", app)
            app.swipeUp()
            app.swipeDown()
            requireLive(appAlerts.isEmpty, "no alert after reconnect cycle \(cycle): \(appAlerts)", app)
        }

        app.tab("Settings").tapWhenReady(timeout: 20)
        let problem = app.staticTexts.matching(NSPredicate(format: "identifier == %@ AND label BEGINSWITH %@", "notifications-problem", "live-b:")).firstMatch
        for _ in 0..<8 where !problem.exists { app.swipeUp() }
        requireLive(problem.waitForExistence(timeout: 150), "Settings shows why notifications could not be set up on live-b", app)
        requireLive(app.buttons["Retry notification setup"].exists, "Settings offers to retry right where it says why", app)
        app.swipeDown()
        requireLive(appAlerts.isEmpty && !app.alerts["Agenthail"].exists, "no alert for a failing notification setup: \(appAlerts)", app)
    }
}
