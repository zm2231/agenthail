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
            requireLive(rows.firstMatch.waitForExistence(timeout: 30), "\(title) is listed from Live A", app)
            XCTAssertEqual(rows.count, 1, "Live A lists \(title) once")
        }
    }

    func testAReplyWrittenOnTheHostShowsInTheOpenConversation() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostID = try pairLiveHost(0, control: control, in: app)
        let row = liveRow("Check the release notes", host: hostID, in: app).firstMatch
        requireLive(row.waitForExistence(timeout: 30), "the conversation is listed", app)
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
        requireLive(row.waitForExistence(timeout: 30), "the conversation is listed", app)

        try control.stop(host: 0)
        requireLive(waitFor(row, value: "Unavailable", timeout: 90), "a stopped host's conversation is marked unavailable", app)
        try control.start(host: 0)
        requireLive(waitFor(row, notValue: "Unavailable", timeout: 90), "the conversation is available again once the host is back", app)

        row.tapWhenReady()
        let seeded = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Done: check the release notes.")).firstMatch
        requireLive(seeded.waitForExistence(timeout: 20), "the conversation opens after the host came back", app)
        let text = "After restart " + UUID().uuidString.prefix(8)
        try control.reply(host: 0, conversation: 0, text: text)
        let reply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
        requireLive(reply.waitForExistence(timeout: 20), "a reply written after the host came back shows live", app)
    }

    private func waitFor(_ element: XCUIElement, value: String? = nil, notValue: String? = nil, timeout: TimeInterval) -> Bool {
        let predicate = value.map { NSPredicate(format: "value == %@", $0) } ?? NSPredicate(format: "exists == true AND value != %@", notValue ?? "")
        return XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: predicate, object: element)], timeout: timeout) == .completed
    }
}
