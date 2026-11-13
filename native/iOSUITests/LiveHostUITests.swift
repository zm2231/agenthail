import XCTest

@MainActor
final class LiveHostUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testPairingWithALiveHostListsItsConversations() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostID = try pairLiveHost(0, control: control, in: app)
        for title in ["Check the release notes", "Tidy the build script"] {
            let rows = liveRow(title, host: hostID, in: app)
            requireLive(reveal(rows.firstMatch, in: app, timeout: 30), "\(title) is listed from Live A", app)
            XCTAssertEqual(rows.count, 1, "Live A lists \(title) once")
        }
    }

    func testAReplyWrittenOnTheHostShowsInTheOpenConversation() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostID = try pairLiveHost(0, control: control, in: app)
        let row = liveRow("Check the release notes", host: hostID, in: app).firstMatch
        requireLive(reveal(row, in: app, timeout: 30), "the conversation is listed", app)
        row.tapWhenReady()
        let seeded = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Done: check the release notes.")).firstMatch
        requireLive(seeded.waitForExistence(timeout: 20), "the conversation opens on its transcript", app)

        let text = "Live reply " + UUID().uuidString.prefix(8)
        try control.reply(host: 0, conversation: 0, text: text)
        let reply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
        requireLive(reply.waitForExistence(timeout: 20), "a reply written on the host shows without a reload", app)
    }

    func testAHostThatGoesDownAndComesBackIsUsableAgainWithoutARelaunch() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostID = try pairLiveHost(0, control: control, in: app)
        let row = liveRow("Check the release notes", host: hostID, in: app).firstMatch
        requireLive(reveal(row, in: app, timeout: 30), "the conversation is listed", app)

        try control.stop(host: 0)
        requireLive(reveal(row, in: app, value: "Unavailable", timeout: 90), "a stopped host's conversation is marked unavailable", app)
        try control.start(host: 0)
        requireLive(reveal(row, in: app, value: "Unavailable", matching: false, timeout: 90), "the conversation is available again once the host is back", app)

        row.tapWhenReady()
        let seeded = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Done: check the release notes.")).firstMatch
        requireLive(seeded.waitForExistence(timeout: 20), "the conversation opens after the host came back", app)
        let text = "After restart " + UUID().uuidString.prefix(8)
        try control.reply(host: 0, conversation: 0, text: text)
        let reply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
        requireLive(reply.waitForExistence(timeout: 20), "a reply written after the host came back shows live", app)
    }

    func testAFinishedTurnLeavesWorkingInTheOpenConversation() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostID = try pairLiveHost(0, control: control, in: app)
        let prompt = "Ship release " + UUID().uuidString.prefix(8)
        try control.startTurn(host: 0, prompt: prompt)
        let row = liveRow(prompt, host: hostID, in: app).firstMatch
        requireLive(reveal(row, in: app, timeout: 30), "the conversation with an open turn is listed", app)
        requireLive(reveal(row, in: app, value: "Working", timeout: 30), "the open turn's conversation is listed as working", app)
        row.tapWhenReady()
        let summary = app.buttons["session-summary"]
        requireLive(summary.waitForExistence(timeout: 20), "the conversation opens", app)
        requireLive(waitFor(summary, labelContains: "Working", timeout: 20), "the open conversation says it is working", app)

        let reply = "Released " + UUID().uuidString.prefix(8)
        let finished = Date()
        try control.finishTurn(host: 0, reply: reply)
        requireLive(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", reply)).firstMatch.waitForExistence(timeout: 20), "the reply shows", app)
        requireLive(waitFor(summary, labelLacks: "Working", timeout: 10), "the conversation stops saying Working once the turn finishes", app)
        print("ios-live: header left Working \(String(format: "%.1f", Date().timeIntervalSince(finished))) s after the turn finished")
    }

    private func waitFor(_ element: XCUIElement, labelContains text: String? = nil, labelLacks missing: String? = nil, timeout: TimeInterval) -> Bool {
        let predicate = text.map { NSPredicate(format: "label CONTAINS %@", $0) } ?? NSPredicate(format: "exists == true AND NOT (label CONTAINS %@)", missing ?? "")
        return XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: predicate, object: element)], timeout: timeout) == .completed
    }
}
