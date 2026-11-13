import XCTest

@MainActor
final class LiveBackAfterReconnectUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testBackReturnsToAnOverflowingSessionsListAfterAnAlertInterruptsTheOpen() throws {
        let control = try LiveControl()
        var alerts = 0
        addUIInterruptionMonitor(withDescription: "app alert") { alert in
            alerts += 1
            alert.buttons.firstMatch.tap()
            return true
        }
        let app = launchForLiveHosts(["--live-test-alert-on-connect"])
        let downHost = try pairLiveHost(0, control: control, in: app)
        let upHost = try pairLiveHost(1, control: control, in: app)
        let downRow = liveRow("Check the release notes", host: downHost, in: app).firstMatch
        let upRow = liveRow("Check the release notes", host: upHost, in: app).firstMatch
        requireLive(reveal(downRow, in: app, timeout: 30) && reveal(upRow, in: app, timeout: 30), "both hosts list their conversations", app)
        try control.stop(host: 0)
        addTeardownBlock { try? control.start(host: 0) }

        var interruptedOpens = 0
        for round in 1...6 {
            app.tab("Sessions").tapWhenReady(timeout: 20)
            let scopeAll = app.buttons["scope-all"]
            for _ in 0..<6 where !scopeAll.isHittable { app.swipeDown() }
            scopeAll.tapWhenReady(timeout: 20)
            app.swipeUp()
            app.swipeDown()
            if reveal(downRow, in: app, timeout: 10), downRow.isHittable {
                downRow.tap()
                _ = app.buttons["session-summary"].waitForExistence(timeout: 10)
                app.backToSessions()
            }
            requireLive(reveal(upRow, in: app, timeout: 60), "the running host's conversation is listed", app)
            let before = alerts
            upRow.tapWhenReady(timeout: 30)
            if alerts > before { interruptedOpens += 1 }

            let text = "Back \(round) " + UUID().uuidString.prefix(6)
            if round == 1 {
                try control.startTurn(host: 1, prompt: "Soak turn")
                sleep(5)
                try control.finishTurn(host: 1, reply: text)
            } else {
                try control.reply(host: 1, conversation: 0, text: text)
                let streamed = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
                requireLive(streamed.waitForExistence(timeout: 30), "the reply streams in round \(round)", app)
            }
            app.navigationBars.buttons.element(boundBy: 0).tap()
            requireLive(upRow.waitForExistence(timeout: 15), "Back returns to the sessions list in round \(round)", app)
            requireLive(app.navigationBars["Sessions"].exists, "the sessions list is on screen in round \(round)", app)

            app.tab("Inbox").tapWhenReady(timeout: 20)
            app.swipeUp()
            XCUIDevice.shared.press(.home)
            sleep(5)
            app.activate()
            requireLive(app.wait(for: .runningForeground, timeout: 20), "the app comes back to the foreground", app)
            try control.stop(host: 1)
            sleep(10)
            try control.start(host: 1)
        }
        requireLive(interruptedOpens > 0, "an alert was dismissed by the tap that opened the conversation at least once", app)
        app.tab("Sessions").tapWhenReady(timeout: 20)
        upRow.tapWhenReady(timeout: 30)
        requireLive(app.buttons["session-summary"].waitForExistence(timeout: 15), "the conversation still opens after every round", app)
    }
}
