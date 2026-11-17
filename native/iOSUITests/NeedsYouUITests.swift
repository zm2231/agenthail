import XCTest

@MainActor
final class NeedsYouUITests: XCTestCase {
    func testAPromptAgenthailCannotReachAndAUsageLimitSayWhatToDo() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-unreachable"]
        app.launch()
        let stranded = app.buttons["needs-you-computer::demo"]
        let limited = app.buttons["needs-you-h0123456789ab::remote-demo-2"]
        XCTAssertTrue(stranded.waitForExistence(timeout: 10))
        XCTAssertTrue(limited.exists)
        let header = app.descendants(matching: .any)["needs-you-header"]
        XCTAssertTrue(header.exists)
        XCTAssertEqual(header.label, "Needs you", "the Inbox badge is the one Needs you count")
        XCTAssertTrue(stranded.label.contains("Waiting on a prompt Agenthail can't reach. Stop and resume to clear it"), stranded.label)
        XCTAssertTrue(limited.label.contains("Usage limit, resets"), limited.label)
        XCTAssertLessThan(stranded.frame.minY, limited.frame.minY)
        let list = XCTAttachment(screenshot: app.screenshot())
        list.name = "Needs you with an unreachable prompt and a usage limit"
        list.lifetime = .keepAlways
        add(list)

        stranded.tap()
        let card = app.otherElements["waiting-elsewhere-preview-approval"]
        XCTAssertTrue(card.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Claude Code is waiting on a prompt Agenthail can't reach"].exists || app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "can't reach")).firstMatch.exists)
        XCTAssertTrue(app.buttons["Stop and resume"].exists)
        let shown = XCTAttachment(screenshot: app.screenshot())
        shown.name = "The card for a prompt Agenthail cannot reach"
        shown.lifetime = .keepAlways
        add(shown)
    }

    func testParkedWorkFromTwoComputersIsOneNamedCardThatTakesAReply() {
        for theme in ["dark", "light"] {
            let app = XCUIApplication()
            app.launchArguments = ["--preview-session", "--preview-app", "--preview-two-computers", "--preview-inbox-cards", "--preview-theme", theme]
            app.launch()
            app.tab("Inbox").tapWhenReady(timeout: 15)
            let card = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH %@", "decision-computer::demo#?Should search")).firstMatch
            XCTAssertTrue(card.waitForExistence(timeout: 10))
            snap(app, "Inbox with parked work from two computers, \(theme)")
            XCTAssertTrue(card.staticTexts["Covers 3 items"].exists)
            XCTAssertEqual(card.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ OR label CONTAINS %@", "waits on you", "computer::")).count, 0, "the card shows no prefix or raw id")
            XCTAssertEqual(app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH %@", "decision-computer::demo#?Should search")).count, 1, "a paired computer's copy joins the card instead of a second row")
            XCTAssertTrue(app.descendants(matching: .any)["decision-computer::codex-demo"].exists, "the ask keeps its own row")
            XCTAssertEqual(app.staticTexts.matching(NSPredicate(format: "label == %@", "Ready to ship")).count, 1, "the ask's title shows once")
            XCTAssertTrue(card.buttons["decision-session-link"].exists, "the card names and links its session")
            XCTAssertTrue(card.buttons["decision-session-link"].label.hasPrefix("@search-indexing"), "the link is the session's alias: \(card.buttons["decision-session-link"].label)")
            XCTAssertTrue(card.staticTexts["Make the build reliable"].exists, "the session's title follows its alias")
            card.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "Should search rank")).firstMatch.tap()
            let reply = app.descendants(matching: .any)["decision-reply"]
            XCTAssertTrue(reply.waitForExistence(timeout: 10))
            XCTAssertTrue(app.descendants(matching: .any)["decision-covered-work"].exists)
            for (id, title) in [("fieldnotes-W8", "Rank search results by title matches before body matches"), ("fieldnotes-W9", "Show matching comment snippets under each search result"), ("fieldnotes-W10", "Weigh comment matches lower than body matches")] {
                XCTAssertTrue(app.staticTexts[title].exists && app.staticTexts[id].exists, "every covered item leads with its title: \(id)")
            }
            reply.tap()
            reply.typeText("Titles first, then body and comments at half weight.")
            snap(app, "Parked work detail with a reply, \(theme)")
            app.buttons["decision-reply-send"].tap()
            XCTAssertTrue(app.descendants(matching: .any)["decision-reply-sent"].waitForExistence(timeout: 10), "the card says the answer went to the session")
            snap(app, "Parked work after the reply, \(theme)")
            app.buttons["decision-not-now"].tap()
            XCTAssertTrue(card.waitForNonExistence(timeout: 10), "Not now takes the card off Needs you")
            snap(app, "Inbox after Not now, \(theme)")
            app.terminate()
        }
    }

    private func snap(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
