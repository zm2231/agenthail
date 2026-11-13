import XCTest

@MainActor
final class LiveHostOutageUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testAStoppedHostShowsWhyWhileTheOtherKeepsWorkingAndItRecovers() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostA = try pairLiveHost(0, control: control, in: app)
        let hostB = try pairLiveHost(1, control: control, in: app)
        let rowA = liveRow("Check the release notes", host: hostA, in: app).firstMatch
        let rowB = liveRow("Check the release notes", host: hostB, in: app).firstMatch
        requireLive(reveal(rowA, in: app, timeout: 30) && reveal(rowB, in: app, timeout: 30), "both hosts list their conversations", app)
        step("1 both hosts listed", app)

        let banner = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "live-a: Refused the connection")).firstMatch
        showAllSessions(in: app)
        requireLive(!banner.exists, "host A has no reason shown while it runs", app)
        rowA.tapWhenReady()
        let seeded = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Done: check the release notes.")).firstMatch
        requireLive(seeded.waitForExistence(timeout: 20), "host A's conversation opens before it stops", app)
        let hostDown = app.descendants(matching: .any).matching(identifier: "composer-host-down").firstMatch
        requireLive(!hostDown.exists, "host A's composer has no outage note while it runs", app)
        let stopped = Date()
        try control.stop(host: 0)
        requireLive(hostDown.waitForExistence(timeout: 90), "host A's composer says the message waits for the host", app)
        print("ios-live: host A's composer says \(hostDown.label) \(String(format: "%.1f", Date().timeIntervalSince(stopped))) s after it stopped")
        step("2a host A's composer says it will send when the host is back", app)
        app.backToSessions()
        showAllSessions(in: app)
        requireLive(banner.waitForExistence(timeout: 90), "the list says why the stopped host can't be reached", app)
        print("ios-live: host A shown unreachable with its reason \(String(format: "%.1f", Date().timeIntervalSince(stopped))) s after it stopped: \(banner.label)")
        requireLive(reveal(rowA, in: app, value: "Unavailable", timeout: 30), "the stopped host's conversation is marked unavailable", app)
        print("ios-live: host A's conversation marked unavailable \(String(format: "%.1f", Date().timeIntervalSince(stopped))) s after it stopped")
        step("2 host A unreachable with its reason", app)

        requireLive(reveal(rowB, in: app, value: "Unavailable", matching: false, timeout: 30), "the running host's conversation stays available", app)
        step("3 host B still lists", app)

        rowB.tapWhenReady()
        requireLive(seeded.waitForExistence(timeout: 20), "host B's conversation opens on its transcript", app)
        step("4 host B opens a conversation", app)
        app.backToSessions()

        let prompt = "Outage turn " + UUID().uuidString.prefix(6)
        try control.startTurn(host: 1, prompt: prompt)
        let turnRow = liveRow(prompt, host: hostB, in: app).firstMatch
        requireLive(reveal(turnRow, in: app, timeout: 30), "host B's working conversation is listed", app)
        turnRow.tapWhenReady()
        let composer = app.textViews["composer-input"]
        requireLive(composer.waitForExistence(timeout: 20), "host B's conversation has a composer", app)
        composer.tapWhenReady()
        let message = "Sent during the outage " + UUID().uuidString.prefix(6)
        composer.typeText(message)
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Send")).firstMatch.tapWhenReady()
        let sent = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Sent to \(prompt)")).firstMatch
        requireLive(sent.waitForExistence(timeout: 20), "host B takes the message", app)
        print("ios-live: host B answered the send: \(sent.label)")
        step("5 host B takes a message", app)

        let command = "git push origin release-" + UUID().uuidString.prefix(6)
        try control.ask(host: 1, command: command)
        let allow = app.buttons["Allow"]
        requireLive(allow.waitForExistence(timeout: 30), "host B's prompt shows in its conversation", app)
        step("6 host B asks for approval", app)
        allow.tapWhenReady()
        var decision: String?
        let deadline = Date().addingTimeInterval(20)
        while decision == nil, Date() < deadline {
            decision = try control.askAnswer(host: 1)
            if decision == nil { Thread.sleep(forTimeInterval: 0.5) }
        }
        requireLive(decision == "allow", "host B's hook got the phone's answer, not \(decision ?? "none")", app)
        print("ios-live: host B's hook got \(decision ?? "none") for \(command)")
        step("7 host B's ask answered", app)

        let reply = "Finished during the outage " + UUID().uuidString.prefix(6)
        try control.finishTurn(host: 1, reply: reply)
        requireLive(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", reply)).firstMatch.waitForExistence(timeout: 20), "host B's reply streams in", app)
        step("8 host B's reply streams in", app)
        app.backToSessions()

        showAllSessions(in: app)
        app.buttons["new-menu"].tapWhenReady(timeout: 20)
        let hostPicker = app.buttons["creation-host-picker"]
        requireLive(hostPicker.waitForExistence(timeout: 20), "the new session form opens", app)
        if !hostPicker.label.contains("live-b") {
            hostPicker.tapWhenReady()
            app.buttons["live-b"].tapWhenReady(timeout: 10)
        }
        requireLive(waitFor(hostPicker, labelContains: "live-b", timeout: 10), "the new session starts on host B", app)
        let launcher = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Launcher")).firstMatch
        requireLive(launcher.waitForExistence(timeout: 30), "host B offers its launchers", app)
        launcher.tapWhenReady()
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "claude-bg")).firstMatch.tapWhenReady(timeout: 10)
        let task = "Started during the outage " + UUID().uuidString.prefix(6)
        let input = app.descendants(matching: .any).matching(NSPredicate(format: "placeholderValue == %@", "What would you like the agent to do?")).firstMatch
        for _ in 0..<8 where !(input.exists && input.isHittable) {
            let form = app.collectionViews.firstMatch
            (form.exists ? form : app).swipeUp()
        }
        input.tapWhenReady(timeout: 10)
        input.typeText(task)
        step("9 starting a session on host B", app)
        app.navigationBars.buttons["Start"].tapWhenReady(timeout: 10)
        let startedRow = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", task)).firstMatch
        requireLive(startedRow.waitForExistence(timeout: 30), "host B started the session", app)
        step("10 host B started a session", app)
        if app.navigationBars.buttons.element(boundBy: 0).exists, !app.navigationBars["Sessions"].exists { app.backToSessions() }

        showAllSessions(in: app)
        requireLive(banner.exists, "host A's reason is still shown before it starts", app)
        let started = Date()
        try control.start(host: 0)
        requireLive(waitForAbsence(banner, timeout: 90), "the reason goes once host A answers", app)
        print("ios-live: host A's reason cleared \(String(format: "%.1f", Date().timeIntervalSince(started))) s after it started")
        requireLive(reveal(rowA, in: app, value: "Unavailable", matching: false, timeout: 60), "host A's conversation is available again", app)
        print("ios-live: host A recovered \(String(format: "%.1f", Date().timeIntervalSince(started))) s after it started")
        step("11 host A recovered", app)
        rowA.tapWhenReady()
        requireLive(seeded.waitForExistence(timeout: 20), "host A's conversation opens again", app)
        step("12 host A opens again", app)
    }

    private func waitFor(_ element: XCUIElement, labelContains text: String, timeout: TimeInterval) -> Bool {
        let matched = expectation(for: NSPredicate(format: "label CONTAINS %@", text), evaluatedWith: element)
        return XCTWaiter().wait(for: [matched], timeout: timeout) == .completed
    }

    private func step(_ name: String, _ app: XCUIApplication) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
        print("ios-live: step \(name) at \(ISO8601DateFormatter().string(from: Date()))")
    }

    private func waitForAbsence(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let gone = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: element)
        return XCTWaiter().wait(for: [gone], timeout: timeout) == .completed
    }
}
