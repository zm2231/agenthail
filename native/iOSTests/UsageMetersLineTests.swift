import SwiftUI
import UIKit
import XCTest
@testable import Agenthail

@MainActor
final class UsageMetersLineTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_790_000_000)

    private var accounts: [UsageAccount] {
        let report = try! JSONDecoder().decode(UsageReport.self, from: Data(UsagePreview.json(now: now).utf8))
        return UsageAccounts.make(report.sets ?? [], now: now)
    }

    private func width(_ accounts: [UsageAccount], at available: CGFloat) -> CGFloat {
        let host = UIHostingController(rootView: UsageMetersLine(accounts: accounts, now: now))
        return ceil(host.sizeThatFits(in: CGSize(width: available, height: .greatestFiniteMagnitude)).width)
    }

    func testTheLineNeverOverflowsAPhoneWidth() {
        let many = accounts + accounts.map { account in
            UsageAccount(id: account.id + "-2", title: account.title + " spare", login: account.login, signIn: account.signIn, host: account.host, hostId: account.hostId + "-2", local: false, provider: account.provider, rootName: account.rootName, root: account.root, meters: account.meters, problem: account.problem, problemDetail: account.problemDetail, asOf: account.asOf)
        }
        for available in [320.0, 375, 393, 430] as [CGFloat] {
            XCTAssertLessThanOrEqual(width(many, at: available), available, "at \(available)pt the usage line must fall back rather than overflow")
        }
    }

    func testAWideRowShowsMoreThanANarrowOne() {
        XCTAssertGreaterThan(width(accounts, at: 2000), width(accounts, at: 320), "with room the line shows every account with its bars")
    }

    func testAnAccountStaysWhenItsLastMeterExpires() {
        var expiring = UsageReport.Meter(provider: "claude", kind: "session", label: "Claude", percent: 40, asOf: ISO8601DateFormatter().string(from: now), aged: false)
        expiring.until = ISO8601DateFormatter().string(from: now.addingTimeInterval(30))
        let feed = UsageFeed()
        feed.ingest([[UsageReport.Set(host: "computer", hostId: "h1", local: true, provider: "claude", account: "me@example.com", primary: true, meters: [expiring])]], at: now)
        XCTAssertEqual(feed.accounts.first?.meters.count, 1)
        feed.show(at: now.addingTimeInterval(60))
        XCTAssertEqual(feed.accounts.map(\.title), ["me"], "an account whose reading passed its window stays, with no meters")
        XCTAssertTrue(feed.accounts[0].meters.isEmpty)
        XCTAssertEqual(feed.now, now.addingTimeInterval(60), "the line's text is built for the same minute")
    }

    func testEveryAccountReadsOutItsFullText() {
        let spoken = accounts.map { $0.spoken(now: now) }
        XCTAssertTrue(spoken[0].hasPrefix("demo@example.com: Claude usage 38 percent"))
        XCTAssertTrue(spoken[2].contains("92 percent"))
        XCTAssertTrue(spoken[3].hasPrefix("API key on build-box: Signed in with an API key"), spoken[3])
        XCTAssertEqual(accounts.map(\.title), ["demo", "Codex", "team", "build-box (API key)", "ops", "lead", "spare"])
        XCTAssertTrue(spoken[6].hasPrefix("spare@example.com via the shared proxy: cooling for"), spoken[6])
        XCTAssertEqual(UsageProxies.strip(accounts).map(\.title), ["demo", "Codex", "team", "build-box (API key)", "shared · lead"], "the line gives a proxy one chip")
        XCTAssertEqual(UsageMeterText.shortLabel(accounts[2].meters[2]), "Week, Fable")
    }
}
