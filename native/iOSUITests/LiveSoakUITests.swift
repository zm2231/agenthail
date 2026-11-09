import XCTest

@MainActor
final class LiveSoakUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testTheAppStaysUpWithOneHostDownThroughListsStreamingInboxAndReconnects() throws {
        let control = try LiveControl()
        let seconds = Double(ProcessInfo.processInfo.environment["AGENTHAIL_SOAK_SECONDS"] ?? "") ?? 300
        addUIInterruptionMonitor(withDescription: "app alert") { [weak self] alert in
            let texts = alert.staticTexts.allElementsBoundByIndex.map(\.label).joined(separator: " | ")
            print("ios-soak: alert: \(texts)")
            let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
            shot.name = "alert: \(texts)"
            shot.lifetime = .keepAlways
            self?.add(shot)
            alert.buttons.firstMatch.tap()
            return true
        }
        let app = launchForLiveHosts()
        let downHost = try pairLiveHost(0, control: control, in: app)
        let upHost = try pairLiveHost(1, control: control, in: app)
        let downRow = liveRow("Check the release notes", host: downHost, in: app).firstMatch
        let upRow = liveRow("Check the release notes", host: upHost, in: app).firstMatch
        requireLive(downRow.waitForExistence(timeout: 30) && upRow.waitForExistence(timeout: 30), "both hosts list their conversations", app)

        try control.stop(host: 0)
        let unavailable = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", "Unavailable"), object: downRow)
        requireLive(XCTWaiter.wait(for: [unavailable], timeout: 90) == .completed, "the stopped host's conversation is marked unavailable", app)

        let end = Date().addingTimeInterval(seconds)
        var iteration = 0
        while Date() < end {
            iteration += 1
            func up(_ step: String) {
                requireLive(app.state == .runningForeground, "the app is running after iteration \(iteration) \(step) (state \(app.state.rawValue))", app)
                print("ios-soak: iteration \(iteration) \(step) ok at \(Int(Date().timeIntervalSince(end) + seconds)) s")
            }

            app.tab("Sessions").tapWhenReady(timeout: 20)
            app.buttons["scope-all"].tapWhenReady(timeout: 20)
            app.swipeUp()
            app.swipeDown()
            up("sessions list")

            if downRow.waitForExistence(timeout: 10), downRow.isHittable {
                downRow.tap()
                _ = app.buttons["session-summary"].waitForExistence(timeout: 10)
                app.backToSessions()
            }
            up("unreachable host's conversation")

            requireLive(upRow.waitForExistence(timeout: 60), "the running host's conversation is listed", app)
            upRow.tapWhenReady(timeout: 30)
            let text = "Soak \(iteration) " + UUID().uuidString.prefix(6)
            try control.reply(host: 1, conversation: 0, text: text)
            let streamed = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
            requireLive(streamed.waitForExistence(timeout: 30), "a reply streams into the open conversation in iteration \(iteration)", app)
            up("streaming")
            app.backToSessions()

            app.tab("Inbox").tapWhenReady(timeout: 20)
            app.swipeUp()
            up("inbox")

            if iteration % 3 == 0 {
                XCUIDevice.shared.press(.home)
                sleep(5)
                app.activate()
                requireLive(app.wait(for: .runningForeground, timeout: 20), "the app comes back to the foreground in iteration \(iteration)", app)
                up("background and foreground")
            }
            if iteration % 5 == 0 {
                try control.stop(host: 1)
                sleep(10)
                try control.start(host: 1)
                up("running host restarted")
            }
        }
        print("ios-soak: \(iteration) iterations over \(Int(seconds)) s with host 0 down")
    }
}
