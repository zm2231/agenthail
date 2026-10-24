import SwiftUI
import UIKit
import XCTest
@testable import Agenthail

@MainActor
final class UsageMetersLineTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_790_000_000)

    private func meter(_ label: String, _ percent: Double, hours: Double, critical: Bool = false) -> UsageReport.Meter {
        let until = ISO8601DateFormatter().string(from: now.addingTimeInterval(hours * 3600))
        return UsageReport.Meter(provider: "claude", kind: "session", label: label, percent: percent, severity: critical ? "critical" : nil, resetsAt: until, asOf: ISO8601DateFormatter().string(from: now), aged: false, until: until)
    }

    private var strip: UsageStrip {
        UsageStrip(
            meters: [meter("Claude", 40, hours: 2.2), meter("Claude week", 62, hours: 76), meter("Codex week", 19, hours: 120)],
            others: [
                .init(id: "a", title: "team", percent: 91, critical: true, spoken: "team@example.com: Claude week usage 91 percent"),
                .init(id: "b", title: "ops · build-box", percent: 12, critical: false, spoken: "ops@example.com on build-box: Claude usage 12 percent"),
            ]
        )
    }

    private func size(_ strip: UsageStrip, at available: CGFloat) -> CGSize {
        UIHostingController(rootView: UsageMetersLine(strip: strip, now: now)).sizeThatFits(in: CGSize(width: available, height: .greatestFiniteMagnitude))
    }

    private func width(at available: CGFloat) -> CGFloat {
        let host = UIHostingController(rootView: UsageMetersLine(strip: strip, now: now))
        return ceil(host.sizeThatFits(in: CGSize(width: available, height: .greatestFiniteMagnitude)).width)
    }

    func testTheLineNeverOverflowsAPhoneWidth() {
        for available in [320.0, 375, 393, 430] as [CGFloat] {
            XCTAssertLessThanOrEqual(width(at: available), available, "at \(available)pt the usage line must fall back rather than overflow")
        }
    }

    func testAFailedReadForThisComputerStaysVisibleBesideOtherAccounts() {
        let failed = UsageStrip(others: strip.others, problem: "claude.ai could not be asked for this computer's account, so its limits are unknown")
        let quiet = UsageStrip(others: strip.others)
        for available in [320.0, 393] as [CGFloat] {
            let shown = size(failed, at: available)
            XCTAssertLessThanOrEqual(ceil(shown.width), available, "at \(available)pt the problem truncates rather than overflowing")
            XCTAssertGreaterThan(shown.height, size(quiet, at: available).height, "the problem takes its own line above the other accounts")
        }
    }

    func testAWideRowShowsMoreThanANarrowOne() {
        XCTAssertGreaterThan(width(at: 2000), width(at: 320), "with room the line shows reset times and other accounts")
    }

    func testTheRowGoesAwayWhenItsLastMeterExpires() {
        var expiring = meter("Claude", 40, hours: 1)
        expiring.until = ISO8601DateFormatter().string(from: now.addingTimeInterval(30))
        let feed = UsageFeed()
        feed.ingest([[UsageReport.Set(host: "computer", hostId: "h1", local: true, provider: "claude", primary: true, meters: [expiring])]], at: now)
        XCTAssertEqual(feed.strip.meters.count, 1)
        feed.show(at: now.addingTimeInterval(60))
        XCTAssertEqual(feed.strip, UsageStrip(), "after the minute tick past expiry there is nothing to show, so the row is gone")
        XCTAssertEqual(feed.now, now.addingTimeInterval(60), "the line's text is built for the same minute")
    }

    func testEveryItemReadsOutItsFullText() {
        let other = strip.others[0]
        XCTAssertEqual(UsageStrip.make([], now: now), UsageStrip())
        XCTAssertTrue(other.spoken.contains("91 percent"))
        XCTAssertTrue(UsageMeterText.spokenLabel(strip.meters[0], now: now).hasPrefix("Claude usage 40 percent"))
    }
}
