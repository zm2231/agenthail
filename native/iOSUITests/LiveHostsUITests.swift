import XCTest

@MainActor
final class LiveHostsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testTwoHostsListTheirOwnConversationsAndOneGoingDownLeavesTheOther() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostA = try pairLiveHost(0, control: control, in: app)
        let hostB = try pairLiveHost(1, control: control, in: app)
        XCTAssertNotEqual(hostA, hostB)
        let rowA = liveRow("Check the release notes", host: hostA, in: app)
        let rowB = liveRow("Check the release notes", host: hostB, in: app)
        requireLive(reveal(rowA.firstMatch, in: app, timeout: 30) && reveal(rowB.firstMatch, in: app, timeout: 30), "each host lists its own conversation", app)
        XCTAssertEqual(rowA.count, 1)
        XCTAssertEqual(rowB.count, 1)

        try control.stop(host: 1)
        requireLive(reveal(rowB.firstMatch, in: app, value: "Unavailable", timeout: 90), "the stopped host's conversation is marked unavailable", app)
        requireLive(reveal(rowA.firstMatch, in: app, timeout: 30), "the host still running is listed", app)
        XCTAssertNotEqual(rowA.firstMatch.value as? String, "Unavailable", "the host still running stays available")
        try control.start(host: 1)
    }

    func testFileSuggestionsComeFromGitOnTheHost() throws {
        let control = try LiveControl()
        let app = launchForLiveHosts()
        let hostA = try pairLiveHost(0, control: control, in: app)
        let row = liveRow("Tidy the build script", host: hostA, in: app).firstMatch
        requireLive(reveal(row, in: app, timeout: 30), "the conversation in a git checkout is listed", app)
        row.tapWhenReady()
        let composer = app.textViews["composer-input"]
        requireLive(composer.waitForExistence(timeout: 20), "the conversation has a composer", app)
        composer.tapWhenReady()
        composer.typeText("look at @")
        let suggestions = app.descendants(matching: .any)["composer-suggestions"]
        requireLive(suggestions.buttons["build.sh"].waitForExistence(timeout: 20), "the host lists the checkout's files", app)
        XCTAssertTrue(suggestions.buttons["README.md"].exists)
        XCTAssertFalse(suggestions.buttons["build.log"].exists, "an ignored file is listed, so the list did not come from git")
    }
}
